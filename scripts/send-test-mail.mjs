// Delivers test mail to a running `make dev-server-start` through the REAL Email Worker (edge/e2e/harness.mjs):
// the Worker forwards, saves the raw mail to the local S3 store (via a small R2 stand-in here) and sends the
// HMAC-signed notify. Then waits until each mail shows up in GET /v1/messages and prints id, business, routing, rule.
//
// Environment (all optional): TO, FROM, SUBJECT, BODY, EML (path to .eml), COUNT (default 1),
//   SAMPLE=invoice|reply|unrouted|newsletter (scripts/samples/<name>.eml), PORT (18090), DEV_TOKEN, S3_URL (http://localhost:9009).
// Run via `make send-test-mail`. Needs Node 22.6+.
import http from "node:http";
import crypto from "node:crypto";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const E = process.env;
const PORT = E.PORT || "18090";
const BASE = `http://127.0.0.1:${PORT}`;
// The ingest listener (only /ingest/v1/notify) is the next port, like scripts/dev-server.sh.
const INGEST_BASE = `http://127.0.0.1:${E.INGEST_PORT || Number(PORT) + 1}`;
const TOKEN = E.DEV_TOKEN || "dev-token-inbox-local-0000";
const S3 = new URL(E.S3_URL || "http://localhost:9009");
const SECRET = E.INGEST_SECRET || "dev-ingest-secret";
const COUNT = Math.max(1, parseInt(E.COUNT || "1", 10) || 1);
const die = (m) => { console.error("send-test-mail: " + m); process.exit(1); };

// ---- build the raw mail(s)
let template;
const file = E.EML || (E.SAMPLE ? path.join(root, "scripts/samples", E.SAMPLE + ".eml") : null);
if (E.SAMPLE && !["invoice", "reply", "unrouted", "newsletter"].includes(E.SAMPLE)) die("SAMPLE must be invoice|reply|unrouted|newsletter");
if (file) {
  if (!fs.existsSync(file)) die("no such file: " + file);
  template = fs.readFileSync(file).toString("latin1");
}
const headerEnd = (s) => { const m = /\r?\n\r?\n/.exec(s); return m ? m.index : s.length; };
const getHeader = (s, name) => new RegExp(`^${name}:[ \\t]*(.*)$`, "im").exec(s.slice(0, headerEnd(s)))?.[1]?.trim();
const addrOf = (v) => (v && (/<([^>]+)>/.exec(v)?.[1] || v.trim())) || undefined;
function setHeader(s, name, value) {
  const he = headerEnd(s);
  let head = s.slice(0, he);
  const re = new RegExp(`^${name}:.*(\\r?\\n[ \\t].*)*`, "im");
  head = re.test(head) ? head.replace(re, `${name}: ${value}`) : `${head}\r\n${name}: ${value}`;
  return head + s.slice(he);
}

function build(i) {
  const tag = `${Date.now().toString(36)}${crypto.randomBytes(3).toString("hex")}`;
  const to = E.TO || addrOf(template && getHeader(template, "To")) || "orders@acme-demo.test";
  const from = E.FROM || addrOf(template && getHeader(template, "From")) || "customer@example.org";
  let raw;
  if (template) {
    raw = template;
    if (E.TO) raw = setHeader(raw, "To", E.TO);
    if (E.FROM) raw = setHeader(raw, "From", E.FROM);
    if (E.SUBJECT) raw = setHeader(raw, "Subject", E.SUBJECT);
    if (E.BODY && !/^content-type:\s*multipart/im.test(raw.slice(0, headerEnd(raw)))) raw = raw.slice(0, headerEnd(raw)) + "\r\n\r\n" + E.BODY + "\r\n";
  } else {
    raw = [`From: ${from}`, `To: ${to}`, `Subject: ${E.SUBJECT || "Test mail from make send-test-mail"}`,
      `Date: ${new Date().toUTCString()}`, "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8", "",
      E.BODY || "Hello, this is a test mail sent through the local Worker harness.", ""].join("\r\n");
  }
  const mid = `<stm-${tag}-${i}@send-test-mail.local>`; // unique, so repeated sends are not de-duplicated
  raw = setHeader(raw, "Message-ID", mid);
  let subject = getHeader(raw, "Subject") || "";
  if (COUNT > 1 && !E.EML) { subject = `${subject} #${i + 1}`; raw = setHeader(raw, "Subject", subject); }
  return { raw: Buffer.from(raw, "latin1"), to: addrOf(getHeader(raw, "To")) || to, from: addrOf(getHeader(raw, "From")) || from, subject, mid };
}

// ---- local S3 (SigV4 PUT, path style) for the R2 stand-in
const hmac = (k, d) => crypto.createHmac("sha256", k).update(d).digest();
const sha = (d) => crypto.createHash("sha256").update(d).digest("hex");
async function s3Put(key, body) {
  const access = E.S3_ACCESS_KEY || "minio", secret = E.S3_SECRET_KEY || "minio12345", bucket = E.S3_BUCKET || "inbox-mail", region = E.S3_REGION || "auto";
  const amz = new Date().toISOString().replace(/[-:]|\.\d{3}/g, ""), day = amz.slice(0, 8);
  const p = `/${bucket}/${key.split("/").map(encodeURIComponent).join("/")}`;
  const hash = sha(body), host = S3.host;
  const signed = "host;x-amz-content-sha256;x-amz-date";
  const canon = `PUT\n${p}\n\nhost:${host}\nx-amz-content-sha256:${hash}\nx-amz-date:${amz}\n\n${signed}\n${hash}`;
  const scope = `${day}/${region}/s3/aws4_request`;
  const sts = `AWS4-HMAC-SHA256\n${amz}\n${scope}\n${sha(canon)}`;
  const kSig = hmac(hmac(hmac(hmac("AWS4" + secret, day), region), "s3"), "aws4_request");
  const auth = `AWS4-HMAC-SHA256 Credential=${access}/${scope}, SignedHeaders=${signed}, Signature=${hmac(kSig, sts).toString("hex")}`;
  const r = await fetch(`${S3.origin}${p}`, { method: "PUT", body, headers: { "x-amz-date": amz, "x-amz-content-sha256": hash, authorization: auth } });
  if (!r.ok) throw new Error(`S3 put HTTP ${r.status} ${await r.text()}`);
}

// ---- API
async function api(p) {
  const r = await fetch(BASE + p, { headers: { authorization: "Bearer " + TOKEN } });
  if (!r.ok) throw new Error(`GET ${p}: HTTP ${r.status}`);
  return r.json();
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const freePort = () => new Promise((res) => { const s = net.createServer().listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); }); });

async function main() {
  try { const r = await fetch(BASE + "/readyz"); if (!r.ok) throw 0; } catch { die(`no server at ${BASE}; run \`make dev-server-start\` first`); }
  let before;
  try { before = new Set((await api("/v1/messages?limit=200")).messages.map((m) => m.id)); } catch (e) { die(String(e) + " (token wrong?)"); }

  const r2 = http.createServer(async (req, res) => {
    const chunks = []; for await (const c of req) chunks.push(c);
    try { await s3Put(new URL(req.url, "http://x").searchParams.get("key"), Buffer.concat(chunks)); res.writeHead(204).end(); }
    catch (e) { console.error(String(e)); res.writeHead(500).end(String(e)); }
  });
  await new Promise((r) => r2.listen(0, "127.0.0.1", r));
  const hport = await freePort();
  const child = spawn(process.execPath, ["--experimental-strip-types", "--no-warnings", path.join(root, "edge/e2e/harness.mjs")], {
    env: { ...E, PORT: String(hport), R2_PUT_URL: `http://127.0.0.1:${r2.address().port}`, FORWARD_TO: "me@gmail.com",
      INGEST_URL: INGEST_BASE + "/ingest/v1/notify", INGEST_SECRET: SECRET },
    stdio: ["ignore", "ignore", "inherit"],
  });
  const stop = () => { child.kill(); r2.close(); };
  process.on("SIGINT", () => { stop(); process.exit(130); });
  let code = 0;
  try {
    for (let i = 0; ; i++) {
      if (child.exitCode !== null) throw new Error("Worker harness exited (needs Node 22.6+)");
      try { await new Promise((ok, no) => net.connect(hport, "127.0.0.1").once("connect", function () { this.end(); ok(); }).once("error", no)); break; }
      catch { if (i > 100) throw new Error("Worker harness did not start"); await sleep(100); }
    }
    const sent = [];
    for (let i = 0; i < COUNT; i++) {
      const m = build(i);
      const r = await fetch(`http://127.0.0.1:${hport}/deliver`, { method: "POST", body: m.raw, headers: { "X-Env-From": m.from, "X-Env-To": m.to } });
      const out = await r.json();
      if (out.error || !out.stored_key) throw new Error("Worker failed: " + JSON.stringify(out));
      console.log(`delivered via Worker: ${m.from} -> ${m.to}  "${m.subject}"  (${out.events.join(", ")})`);
      sent.push(m);
    }
    // wait for the ingest job to turn each notify into a message
    const pending = new Map(sent.map((m) => [m.mid, m]));
    const claimed = new Set();
    const deadline = Date.now() + 30000;
    while (pending.size && Date.now() < deadline) {
      const list = (await api("/v1/messages?limit=200")).messages.filter((m) => !before.has(m.id) && !claimed.has(m.id));
      for (const [mid, m] of pending) {
        const hit = list.find((x) => x.subject === m.subject && (x.from_addr || "").toLowerCase() === m.from.toLowerCase() && !claimed.has(x.id));
        if (!hit) continue;
        claimed.add(hit.id); pending.delete(mid);
        const d = await api("/v1/messages/" + hit.id);
        const det = d.details || d.message?.details || {};
        const unrouted = hit.business_id == null || hit.route === "unrouted";
        console.log(`  appeared: id=${hit.id}\n    business : ${det.business?.name ?? "(none)"}\n    routing  : ${unrouted ? "unrouted" : "routed"} (route=${hit.route})\n    rule     : ${det.rule_name ?? "(no rule matched)"}\n    attach.  : ${hit.attachment_count}`);
      }
      if (pending.size) await sleep(500);
    }
    if (pending.size) throw new Error(`${pending.size} mail(s) did not appear within 30 s; see server/bin/dev-server.log`);
  } catch (e) { console.error("send-test-mail: " + e.message); code = 1; }
  stop();
  process.exit(code);
}
main();
