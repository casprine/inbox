import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createHmac } from "node:crypto";
import worker, { sign, ulid } from "../src/index";

const rawBytes = new TextEncoder().encode("Subject: hi\r\n\r\nbody");

function setup(opts: { put?: () => Promise<unknown>; forward?: () => Promise<void> } = {}) {
  const calls: string[] = [];
  const message = {
    from: "a@x.com", to: "b@biz.com", rawSize: rawBytes.length,
    headers: new Headers({ "message-id": "<1@x>" }),
    raw: new ReadableStream({ start(c) { c.enqueue(rawBytes); c.close(); } }),
    forward: vi.fn(async () => { calls.push("forward"); await opts.forward?.(); }),
  } as unknown as ForwardableEmailMessage;
  const put = vi.fn(async (_k: string, v: unknown) => {
    calls.push("put");
    if (v instanceof ReadableStream) await new Response(v).arrayBuffer();
    return opts.put?.();
  });
  const env = { RAW: { put } as unknown as R2Bucket, FORWARD_TO: "me@g.com", INGEST_URL: "https://s/ingest", INGEST_SECRET: "sek" };
  const waits: Promise<unknown>[] = [];
  const ctx = { waitUntil: (p: Promise<unknown>) => waits.push(p) } as unknown as ExecutionContext;
  return { message, env, ctx, put, calls, waits };
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout"] });
  vi.stubGlobal("FixedLengthStream", class {
    private t = new TransformStream();
    readable = this.t.readable; writable = this.t.writable;
  });
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

async function run(s: ReturnType<typeof setup>) {
  await worker.email(s.message, s.env, s.ctx);
  let finished = false;
  const done = Promise.all(s.waits).then(() => { finished = true; });
  while (!finished) {
    await vi.advanceTimersByTimeAsync(1000);
    await new Promise<void>((r) => setImmediate(r)); // let real async crypto settle
  }
  await done;
}

describe("email()", () => {
  it("forwards first, stores, then notifies with a valid signature", async () => {
    const fetchMock = vi.fn(async () => new Response("ok"));
    vi.stubGlobal("fetch", fetchMock);
    const s = setup();
    await run(s);
    expect(s.calls).toEqual(["forward", "put"]);
    expect(s.put.mock.calls[0][0]).toMatch(/^raw\/\d{4}\/\d{2}\/[0-9A-Z]{26}\.eml$/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("https://s/ingest");
    const h = init.headers as Record<string, string>;
    const body = init.body as string;
    expect(h["X-Inbox-Signature"]).toBe(createHmac("sha256", "sek").update(`${h["X-Inbox-Timestamp"]}.${body}`).digest("hex"));
    const p = JSON.parse(body);
    expect(p).toMatchObject({ env_from: "a@x.com", env_to: "b@biz.com", message_id: "<1@x>", raw_size: rawBytes.length });
    expect(p.key).toBe(s.put.mock.calls[0][0]);
  });

  it("sends store_failed notify when R2 put fails, and does not throw", async () => {
    const fetchMock = vi.fn(async () => new Response("ok"));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "error").mockImplementation(() => {});
    const s = setup({ put: async () => { throw new Error("r2 down"); } });
    await run(s);
    const p = JSON.parse((fetchMock.mock.calls[0] as any)[1].body);
    expect(p).toMatchObject({ key: null, reason: "store_failed" });
  });

  it("retries notify 3 times then gives up without throwing", async () => {
    const fetchMock = vi.fn(async () => new Response("no", { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "error").mockImplementation(() => {});
    const s = setup();
    await run(s);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("retries after network error and stops on success", async () => {
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new Error("net"))
      .mockResolvedValue(new Response("ok"));
    vi.stubGlobal("fetch", fetchMock);
    await run(setup());
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("still stores when forward fails, and never rethrows", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("ok")));
    vi.spyOn(console, "error").mockImplementation(() => {});
    const s = setup({ forward: async () => { throw new Error("fwd"); } });
    await expect(run(s)).resolves.toBeUndefined();
    expect(s.calls).toEqual(["forward", "put"]);
  });

  it("buffers when FixedLengthStream is unavailable", async () => {
    vi.stubGlobal("FixedLengthStream", undefined);
    vi.stubGlobal("fetch", vi.fn(async () => new Response("ok")));
    const s = setup();
    await run(s);
    expect(s.put.mock.calls[0][1]).toBeInstanceOf(ArrayBuffer);
  });
});

describe("helpers", () => {
  it("sign is hex HMAC-SHA256 of timestamp.body", async () => {
    expect(await sign("k", "123", "{}")).toBe(createHmac("sha256", "k").update("123.{}").digest("hex"));
  });
  it("ulid is 26 chars and time-sortable", () => {
    expect(ulid(1000)).toHaveLength(26);
    expect(ulid(1000).slice(0, 10) < ulid(2000).slice(0, 10)).toBe(true);
  });
});
