// Isolated P03 browser acceptance against production dist + a real local test API.
// node scripts/gantt-test-proxy.mjs [5184] [8013]
// Bind only loopback. /__p03 is test-only fault injection, never bundled into dist.
import { createServer, request } from 'node:http';
import { readFileSync, existsSync } from 'node:fs';
import { resolve, extname, sep } from 'node:path';
const port = Number(process.argv[2] || 5184), apiPort = Number(process.argv[3] || 8013);
if (![port, apiPort].every(p => Number.isInteger(p) && p > 1024 && p < 65536)) throw new Error('Invalid local test port');
const root = resolve('dist');
let faults = { write: 'normal', read: 'normal' };
const patches = [], reads = [], projectQueries = [];
const bodyOf = async req => { const chunks = []; for await (const chunk of req) chunks.push(chunk); return Buffer.concat(chunks); };
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
createServer(async (req, res) => {
  try {
    const path = new URL(req.url, `http://127.0.0.1:${port}`).pathname;
    if (path === '/__p03') {
      if (req.method === 'POST') {
        const next = JSON.parse((await bodyOf(req)).toString());
        if (!['normal', 'slow', 'drop', 'malformed'].includes(next.write) || !['normal', 'slow', 'fail'].includes(next.read)) throw new Error('Invalid fault mode');
        faults = next;
      }
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ faults, patches, reads, projectQueries })); return;
    }
    if (path.startsWith('/api/') || path === '/healthz') {
      const body = await bodyOf(req);
      const write = req.method === 'PATCH' && /^\/api\/v1\/tasks\//.test(path);
      const read = req.method === 'GET' && path.startsWith('/api/v1/plan-views/');
      const projectQuery = req.method === 'POST' && path === '/api/v1/projects/query';
      const mode = { ...faults };
      if (write) patches.push({ path, body: JSON.parse(body.toString()), at: Date.now() });
      if (read) reads.push({ path, at: Date.now() });
      if (projectQuery) projectQueries.push({ body: JSON.parse(body.toString()), at: Date.now() });
      if ((write && mode.write === 'slow') || ((read || projectQuery) && mode.read === 'slow')) await delay(1800);
      if (read && mode.read === 'fail') { res.writeHead(503, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error: { code: 'internal_error', message: 'P03 injected read failure' } })); return; }
      const upstream = request({ host: '127.0.0.1', port: apiPort, path: req.url, method: req.method, headers: { ...req.headers, host: `127.0.0.1:${apiPort}` } }, response => {
        // Lose the upstream result after commit. Return an explicit gateway failure
        // so Chromium does not transparently replay a request on a closed socket.
        if (write && mode.write === 'drop') { response.resume(); response.on('end', () => {
          res.writeHead(502, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: { code: 'internal_error', message: 'P03 upstream result unavailable after commit' } }));
        }); return; }
        if (write && mode.write === 'malformed') { response.resume(); response.on('end', () => { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end('{}'); }); return; }
        res.writeHead(response.statusCode, response.headers); response.pipe(res);
      });
      upstream.on('error', () => { res.writeHead(502); res.end(); }); upstream.end(body); return;
    }
    let file = resolve(root, '.' + decodeURIComponent(path));
    if (file !== root && !file.startsWith(root + sep)) { res.writeHead(403); res.end(); return; }
    if (!existsSync(file) || !extname(file)) file = resolve(root, 'index.html');
    const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.woff2': 'font/woff2', '.woff': 'font/woff' };
    res.setHeader('Content-Type', types[extname(file)] || 'application/octet-stream'); res.end(readFileSync(file));
  } catch (error) { res.writeHead(500); res.end(String(error)); }
}).listen(port, '127.0.0.1', () => console.log(`P03 dist acceptance: http://127.0.0.1:${port} -> API ${apiPort}`));
