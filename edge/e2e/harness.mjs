// Runs the REAL Email Worker (edge/src/index.ts, unmodified) outside Cloudflare so `make e2e` can drive it.
//
//   node --experimental-strip-types edge/e2e/harness.mjs
//
// It plays Cloudflare Email Routing: POST /deliver with the raw message as the body and the envelope in
// X-Env-From / X-Env-To builds a ForwardableEmailMessage and calls worker.email(). The R2 binding is a stub that hands
// the object to the e2e driver (R2_PUT_URL), which writes it to the local S3 store. The Worker's own fetch() goes to the
// real inboxd (INGEST_URL), signed with INGEST_SECRET.
//
// Env: PORT, R2_PUT_URL, FORWARD_TO, INGEST_URL, INGEST_SECRET.
// Request header X-Fail-R2: 1 makes the R2 put throw (the Worker must report store_failed and still forward).
import http from "node:http";
import worker from "../src/index.ts";

const { PORT, R2_PUT_URL, FORWARD_TO, INGEST_URL, INGEST_SECRET } = process.env;
for (const [k, v] of Object.entries({ PORT, R2_PUT_URL, FORWARD_TO, INGEST_URL, INGEST_SECRET })) {
  if (!v) { console.error(`missing ${k}`); process.exit(2); }
}

// Cloudflare's FixedLengthStream: a TransformStream that requires a known length. A passthrough is enough here and
// makes the Worker take its streaming path, as it does in production.
globalThis.FixedLengthStream = class {
  constructor(len) { this.len = len; const t = new TransformStream(); this.readable = t.readable; this.writable = t.writable; }
};

function parseHeaders(raw) {
  const text = new TextDecoder("latin1").decode(raw);
  const head = text.split(/\r?\n\r?\n/, 1)[0].replace(/\r?\n[ \t]+/g, " ");
  const h = new Headers();
  for (const line of head.split(/\r?\n/)) {
    const i = line.indexOf(":");
    if (i > 0) h.append(line.slice(0, i).trim(), line.slice(i + 1).trim());
  }
  return h;
}

async function readBody(req) {
  const chunks = [];
  for await (const c of req) chunks.push(c);
  return Buffer.concat(chunks);
}

const server = http.createServer(async (req, res) => {
  if (req.method !== "POST" || req.url !== "/deliver") { res.writeHead(404).end(); return; }
  const raw = await readBody(req);
  const events = [];
  let storedKey = null;
  const failR2 = req.headers["x-fail-r2"] === "1";

  const message = {
    from: req.headers["x-env-from"],
    to: req.headers["x-env-to"],
    rawSize: raw.length,
    headers: parseHeaders(raw),
    raw: new ReadableStream({ start(c) { c.enqueue(new Uint8Array(raw)); c.close(); } }),
    async forward(addr) { events.push("forward:" + addr); },
  };
  const env = {
    FORWARD_TO, INGEST_URL, INGEST_SECRET,
    RAW: {
      async put(key, value) {
        events.push("put:" + key);
        if (failR2) throw new Error("simulated R2 outage");
        const body = value instanceof ReadableStream ? Buffer.from(await new Response(value).arrayBuffer()) : Buffer.from(value);
        const r = await fetch(`${R2_PUT_URL}?key=${encodeURIComponent(key)}`, { method: "PUT", body });
        if (!r.ok) throw new Error("r2 stub: HTTP " + r.status);
        storedKey = key;
      },
    },
  };
  const waits = [];
  const ctx = { waitUntil: (p) => waits.push(p), passThroughOnException() {} };
  let error = null;
  try {
    await worker.email(message, env, ctx);
    await Promise.all(waits); // the signed notify (with its retries) runs here, like waitUntil does in production
  } catch (e) { error = String(e); }
  res.writeHead(200, { "content-type": "application/json" });
  res.end(JSON.stringify({ events, stored_key: storedKey, error }));
});

server.listen(Number(PORT), "127.0.0.1", () => console.log("worker harness listening on " + PORT));
process.on("SIGTERM", () => server.close(() => process.exit(0)));
