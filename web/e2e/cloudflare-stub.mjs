#!/usr/bin/env node
// Minimal in-memory stand-in for the Cloudflare DNS records API, used by the
// browser E2E workflow so the api-server's managed Entry DNS reconciler can
// run without the real Cloudflare API or a real token.
//
//   node web/e2e/cloudflare-stub.mjs [PORT]   (default 18090, loopback only)
//
// Implements exactly what api-server/internal/services/cloudflare_dns_*.go
// calls: list (name.exact, type, page), create, update and delete of records
// under /client/v4/zones/:zone/dns_records. Records live only in memory.
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";

const port = Number(process.argv[2] || 18090);
const records = new Map(); // id -> record
const prefix = "/client/v4/zones/";

const send = (res, status, body) => {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
};
const ok = (res, result, extra = {}) => send(res, 200, { success: true, errors: [], messages: [], result, ...extra });
const fail = (res, status, code) => send(res, status, { success: false, errors: [{ code }], messages: [], result: null });

const readBody = (req) => new Promise((resolve, reject) => {
  let data = "";
  req.on("data", (chunk) => { data += chunk; if (data.length > 1 << 16) reject(new Error("body too large")); });
  req.on("end", () => { try { resolve(data ? JSON.parse(data) : {}); } catch (error) { reject(error); } });
  req.on("error", reject);
});

const writable = (body) => body && typeof body.name === "string" && body.type === "A" &&
  typeof body.content === "string" && Number.isInteger(body.ttl);

createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  if (!req.headers.authorization?.startsWith("Bearer ")) return fail(res, 401, 10000);
  if (!url.pathname.startsWith(prefix)) return fail(res, 404, 7003);
  const [zone, collection, id, ...rest] = url.pathname.slice(prefix.length).split("/");
  if (!zone || collection !== "dns_records" || rest.length) return fail(res, 404, 7003);
  try {
    if (req.method === "GET" && !id) {
      const name = url.searchParams.get("name.exact");
      const type = url.searchParams.get("type");
      const page = Number(url.searchParams.get("page") || 1);
      const matches = [...records.values()].filter((r) => r.zone === zone &&
        (!name || r.name.toLowerCase() === name.toLowerCase()) && (!type || r.type === type));
      const result = page === 1 ? matches.map(({ zone: _zone, ...r }) => r) : [];
      return ok(res, result, { result_info: { page, per_page: 100, count: result.length, total_count: matches.length, total_pages: matches.length ? 1 : 0 } });
    }
    if (req.method === "POST" && !id) {
      const body = await readBody(req);
      if (!writable(body)) return fail(res, 400, 9000);
      const record = { id: randomUUID().replaceAll("-", ""), zone, name: body.name, type: "A", content: body.content, ttl: body.ttl, proxied: !!body.proxied, comment: body.comment || "" };
      records.set(record.id, record);
      const { zone: _zone, ...result } = record;
      return ok(res, result);
    }
    if (req.method === "PUT" && id) {
      const existing = records.get(id);
      if (!existing || existing.zone !== zone) return fail(res, 404, 81044);
      const body = await readBody(req);
      if (!writable(body)) return fail(res, 400, 9000);
      Object.assign(existing, { name: body.name, content: body.content, ttl: body.ttl, proxied: !!body.proxied, comment: body.comment || "" });
      const { zone: _zone, ...result } = existing;
      return ok(res, result);
    }
    if (req.method === "DELETE" && id) {
      const existing = records.get(id);
      if (!existing || existing.zone !== zone) return fail(res, 404, 81044);
      records.delete(id);
      return ok(res, { id });
    }
    return fail(res, 405, 10000);
  } catch {
    return fail(res, 400, 9000);
  }
}).listen(port, "127.0.0.1", () => console.log(`cloudflare stub on http://127.0.0.1:${port}/client/v4`));
