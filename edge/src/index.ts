export interface Env {
  RAW: R2Bucket;
  FORWARD_TO: string;
  INGEST_URL: string;
  INGEST_SECRET: string;
}

const RETRY_DELAYS_MS = [0, 1000, 4000]; // 3 attempts

export default {
  async email(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<void> {
    try {
      await message.forward(env.FORWARD_TO); // always first, nothing else may block delivery
    } catch (e) {
      console.error("forward failed", e);
    }

    try {
      const d = new Date();
      const key = `raw/${d.getUTCFullYear()}/${String(d.getUTCMonth() + 1).padStart(2, "0")}/${ulid(d.getTime())}.eml`;
      const meta = {
        env_from: message.from,
        env_to: message.to,
        raw_size: message.rawSize,
        received_at: d.toISOString(),
        message_id: message.headers.get("message-id") ?? "",
      };
      try {
        await store(env, key, message);
        ctx.waitUntil(notify(env, { key, ...meta }));
      } catch (e) {
        console.error("store failed", e);
        ctx.waitUntil(notify(env, { key: null, reason: "store_failed", ...meta }));
      }
    } catch (e) {
      console.error("ingest failed", e); // never rethrow
    }
  },
};

async function store(env: Env, key: string, message: ForwardableEmailMessage): Promise<void> {
  const customMetadata = { env_from: message.from, env_to: message.to };
  if (typeof FixedLengthStream === "function") {
    const fl = new FixedLengthStream(message.rawSize);
    const piped = message.raw.pipeTo(fl.writable);
    await Promise.all([env.RAW.put(key, fl.readable, { customMetadata }), piped]);
  } else {
    const buf = await new Response(message.raw).arrayBuffer();
    await env.RAW.put(key, buf, { customMetadata });
  }
}

/** POSTs a signed JSON body; retries up to 3 times. Never throws. */
export async function notify(env: Env, payload: Record<string, unknown>): Promise<void> {
  const body = JSON.stringify(payload);
  const timestamp = String(Math.floor(Date.now() / 1000));
  const signature = await sign(env.INGEST_SECRET, timestamp, body);
  for (const delay of RETRY_DELAYS_MS) {
    if (delay) await new Promise((r) => setTimeout(r, delay));
    try {
      const res = await fetch(env.INGEST_URL, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "X-Inbox-Timestamp": timestamp,
          "X-Inbox-Signature": signature,
        },
        body,
      });
      if (res.ok) return;
      if (res.status >= 400 && res.status < 500 && res.status !== 429) return; // permanent
    } catch {
      // network error: retry
    }
  }
  console.error("notify gave up", payload.key);
}

/** Hex HMAC-SHA256 of `${timestamp}.${body}`. */
export async function sign(secret: string, timestamp: string, body: string): Promise<string> {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const mac = await crypto.subtle.sign("HMAC", key, enc.encode(`${timestamp}.${body}`));
  return [...new Uint8Array(mac)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
export function ulid(ms: number): string {
  let t = "";
  for (let i = 0; i < 10; i++, ms = Math.floor(ms / 32)) t = CROCKFORD[ms % 32] + t;
  const r = crypto.getRandomValues(new Uint8Array(16));
  return t + [...r].map((b) => CROCKFORD[b % 32]).join("");
}
