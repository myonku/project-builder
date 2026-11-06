// Real Chromium + the Go HTTP harness (isolated PostgreSQL/Redis). No mocked business API.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { resolve, join, relative } from 'node:path';
import { tmpdir } from 'node:os';
const origin = process.argv[2]; assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/);
const out = resolve('.verify/p05'); mkdirSync(out, { recursive: true });
const profile = mkdtempSync(join(tmpdir(), 'worklane-p05-'));
const chrome = spawn(process.env.CHROME_PATH || 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  ['--headless=new', '--no-first-run', '--disable-gpu', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'],
  { windowsHide: true, stdio: 'ignore' });
let ws, send, evaluate; let checks = 0; const errors = []; const network = [];
const pause = ms => new Promise(r => setTimeout(r, ms));
const check = (value, label) => { assert.ok(value, label); checks++; console.log('PASS ' + label); };
try {
  const portFile = join(profile, 'DevToolsActivePort');
  for (let i = 0; !existsSync(portFile) && i < 100; i++) await pause(100);
  const port = readFileSync(portFile, 'utf8').split('\n')[0];
  const target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find(t => t.type === 'page');
  ws = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map(); let nextId = 1;
  ws.onmessage = event => {
    const msg = JSON.parse(event.data);
    if (msg.id) { const p = pending.get(msg.id); pending.delete(msg.id); if (msg.error) p.reject(msg.error); else p.resolve(msg.result); }
    if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params.exceptionDetails.text);
    if (msg.method === 'Network.requestWillBeSent' && msg.params.request.url.includes('/api/v1/')) network.push(msg.params.request.url);
  };
  await new Promise((r, reject) => { ws.onopen = r; ws.onerror = reject; });
  send = (method, params = {}) => new Promise((resolve, reject) => { const id = nextId++; pending.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params })); });
  evaluate = async expression => {
    const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails)); return r.result.value;
  };
  const wait = async expression => { for (let i = 0; i < 200; i++) { if (await evaluate(expression)) return; await pause(50); } throw new Error('Timed out: ' + expression + '\n' + await evaluate('document.body.innerText')); };
  const textHas = text => `document.body.innerText.includes(${JSON.stringify(text)})`;
  const click = async text => {
    await wait(`[...document.querySelectorAll('button')].some(e=>e.textContent.trim()===${JSON.stringify(text)}&&!e.disabled&&e.getClientRects().length)`);
    check(await evaluate(`(()=>{const e=[...document.querySelectorAll('button')].filter(e=>e.textContent.trim()===${JSON.stringify(text)}&&!e.disabled&&e.getClientRects().length).at(-1);if(!e)return false;e.click();return true})()`), 'click ' + text);
    await pause(80);
  };
  const field = async (label, value) => {
    check(await evaluate(`(()=>{const l=[...document.querySelectorAll('label')].find(e=>e.textContent.trim()===${JSON.stringify(label)});const e=l&&document.getElementById(l.htmlFor);if(!e)return false;const p=e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(p,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}));return true})()`), 'fill ' + label);
  };
  const rowClick = async (name, button) => {
    await evaluate(`document.querySelectorAll('.MuiDataGrid-virtualScroller').forEach(e=>e.scrollLeft=0)`);
    await wait(`!![...document.querySelectorAll('[role=row]')].find(e=>e.textContent.includes(${JSON.stringify(name)}))`);
    const id = await evaluate(`[...document.querySelectorAll('[role=row]')].find(e=>e.textContent.includes(${JSON.stringify(name)})).getAttribute('data-id')`);
    const buttonExpr = `(()=>{const row=document.querySelector('[data-id="'+${JSON.stringify(id)}+'"]');return row&&[...row.querySelectorAll('button')].find(e=>e.textContent.trim()===${JSON.stringify(button)}&&!e.disabled)})()`;
    if (!await evaluate(`!!${buttonExpr}`)) await evaluate(`document.querySelectorAll('.MuiDataGrid-virtualScroller').forEach(e=>e.scrollLeft=e.scrollWidth)`);
    await wait(`!!${buttonExpr}`);
    check(await evaluate(`(()=>{const b=${buttonExpr};if(!b)return false;b.click();return true})()`), 'row ' + name + ': ' + button);
  };
  const go = async path => { await evaluate('window.__p05Navigating=true'); await send('Page.navigate', { url: origin + path }); await wait('!window.__p05Navigating && document.readyState==="complete" && !!document.querySelector("main")'); };
  const as = async role => {
    await send('Network.clearBrowserCookies');
    await send('Network.setCookie', { name: process.env.P05_COOKIE_NAME, value: process.env['P05_' + role], url: origin });
  };
  const screenshot = async name => { const r = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); writeFileSync(join(out, name + '.png'), Buffer.from(r.data, 'base64')); };
  const api = (path, method = 'GET', body) => evaluate(`(async()=>{const r=await fetch('/api/v1'+${JSON.stringify(path)},{method:${JSON.stringify(method)},headers:{'Content-Type':'application/json'},body:${body === undefined ? 'undefined' : JSON.stringify(JSON.stringify(body))}});return{status:r.status,body:await r.json()}})()`);
  const injectLostResponse = async path => evaluate(`(()=>{const real=window.fetch;let lost=false;window.fetch=async (...args)=>{const r=await real(...args);if(!lost&&String(args[0])===${JSON.stringify('/api/v1' + path)}&&args[1]?.method==='POST'&&r.ok){lost=true;throw new TypeError('P05 injected lost response')}return r}})()`);
  const openApplication = async (name, reason = '组织跨项目协作') => {
    await click('申请新建工作组'); await wait('!!document.querySelector("#group-creation-form-title")');
    await field('工作组名称', name); await field('说明', '申请说明'); await field('申请理由', reason);
  };
  const submitApplication = async name => {
    await openApplication(name); await click('提交申请'); await wait('!document.querySelector("#group-creation-form-title")');
    await wait(textHas(name));
  };
  await send('Runtime.enable'); await send('Page.enable'); await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await as('APPLICANT'); await go('/groups'); await wait(textHas('你还没有加入工作组'));
  check((await api('/groups', 'POST', { name: '绕过' })).status === 403, 'member direct create denied');
  await openApplication('P05审批组'); await click('提交申请');
  await wait('!document.querySelector("#group-creation-form-title")'); await wait(textHas('P05审批组'));
  let mine = (await api('/me/group-creation-requests/query', 'POST', {})).body.data;
  const approvedID = mine[0].id;
  check(mine[0].status === 'pending' && mine[0].group_id === null, 'pending is not a group');
  check(!(await api('/groups/query', 'POST', {})).body.data.length, 'pending does not join group');
  await screenshot('applicant-pending-desktop');
  // Commit on the real server, then discard only the response seen by the application.
  await injectLostResponse('/group-creation-requests');
  await openApplication('P05丢失响应组'); await click('提交申请'); await wait(textHas('提交结果待核对'));
  const saved = await evaluate(`JSON.parse(Object.values(sessionStorage).find(v=>v.includes('P05丢失响应组')))`);
  check(!!saved?.key, 'uncertain submission preserves idempotency key');
  await click('关闭'); await send('Page.reload'); await wait(textHas('申请新建工作组')); await click('申请新建工作组');
  await wait(textHas('提交结果待核对')); check(await evaluate('document.querySelectorAll(".MuiDialog-paper input:disabled").length>0'), 'restored uncertain body is locked');
  await click('核对并重试'); await wait('!document.querySelector("#group-creation-form-title")');
  mine = (await api('/me/group-creation-requests/query', 'POST', {})).body.data;
  check(mine.filter(r => r.name === 'P05丢失响应组').length === 1, 'lost-response replay creates one request');
  await submitApplication('P05拒绝组');

  await as('OTHER'); await go('/groups'); await click('新建申请'); await wait(textHas('当前筛选共 0 条'));
  check((await api('/group-creation-requests/' + approvedID)).status === 404, 'unrelated user cannot read request');
  check(!(await evaluate('document.body.innerText')).includes('P05审批组'), 'account switch does not reuse applicant data');
  await as('MANAGER'); await go('/groups'); await wait(textHas('既有管理组'));
  check((await api('/group-creation-requests/' + approvedID + '/review', 'POST', { status: 'approved' })).status === 403, 'group manager cannot approve group creation');

  await as('ADMIN'); await go('/admin/groups'); await click('新建申请'); await wait(textHas('新建申请审核'));
  await wait(textHas('待审核共 3 条')); await screenshot('admin-pending-desktop');
  await rowClick('P05审批组', '查看并审核'); await wait(textHas('申请理由：组织跨项目协作'));
  // A stale reviewer sees conflict and the authoritative terminal state after another reviewer wins.
  const detail = (await api('/group-creation-requests/' + approvedID)).body.data;
  check((await api('/group-creation-requests/' + approvedID + '/review', 'POST', { status: 'approved', expected_updated_at: detail.updated_at })).status === 200, 'concurrent reviewer approves');
  await click('通过申请'); await click('通过并创建'); await wait(textHas('已通过'));
  await wait('!document.querySelector(".MuiDialog-paper button.MuiButton-contained")');
  check((await api('/group-creation-requests/' + approvedID)).body.data.status === 'approved', 'stale review resolves to approved');
  await click('关闭');
  await rowClick('P05拒绝组', '查看并审核'); await wait(textHas('申请理由：组织跨项目协作')); await click('拒绝申请');
  await wait(textHas('请填写 1–2000 个字符的拒绝理由')); await field('拒绝理由', '请明确协作范围'); await click('拒绝申请'); await wait(textHas('拒绝理由：请明确协作范围'));
  await screenshot('admin-rejected-desktop'); await click('关闭');
  await rowClick('P05丢失响应组', '查看并审核'); await wait(textHas('申请理由：组织跨项目协作'));
  const lossID = mine.find(r => r.name === 'P05丢失响应组').id;
  await injectLostResponse('/group-creation-requests/' + lossID + '/review'); await click('通过申请'); await click('通过并创建');
  await wait(textHas('已通过')); check((await api('/group-creation-requests/' + lossID)).body.data.group_id, 'lost approval response recovers existing group'); await click('关闭');

  await as('APPLICANT'); await go('/groups'); await wait(textHas('P05审批组')); await click('新建申请'); await wait(textHas('P05拒绝组'));
  await rowClick('P05拒绝组', '查看申请'); await wait(textHas('拒绝理由：请明确协作范围')); await click('重新申请');
  await wait('!!document.querySelector("#group-creation-form-title")');
  check(await evaluate('[...document.querySelectorAll(".MuiDialog-paper input")].some(e=>e.value==="P05拒绝组")'), 'resubmission copies snapshot');
  await field('申请理由', '明确后的项目协作范围'); await click('提交申请'); await wait('!document.querySelector("#group-creation-form-title")');
  mine = (await api('/me/group-creation-requests/query', 'POST', {})).body.data;
  check(mine.filter(r => r.name === 'P05拒绝组').length === 2, 'resubmission preserves rejected history');
  await rowClick('P05审批组', '进入工作组'); await wait(textHas('基本信息')); await screenshot('approved-group-desktop');
  // Mobile uses real form + list + approval, not a static responsive snapshot.
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await go('/groups'); await submitApplication('P05手机组');
  check(await evaluate('document.documentElement.scrollWidth<=innerWidth'), 'mobile applicant page fits viewport'); await screenshot('applicant-mobile');
  await as('ADMIN'); await go('/admin/groups'); await click('新建申请'); await wait(textHas('P05手机组'));
  await rowClick('P05手机组', '查看并审核'); await wait(textHas('申请理由：组织跨项目协作')); await click('通过申请'); await click('通过并创建'); await wait(textHas('已通过'));
  check(await evaluate('document.documentElement.scrollWidth<=innerWidth'), 'mobile approval dialog fits viewport'); await screenshot('admin-mobile'); await click('关闭');
  await as('APPLICANT'); await go('/groups'); await wait(textHas('P05手机组')); await click('新建申请'); await wait(textHas('P05手机组')); await rowClick('P05手机组', '进入工作组'); await wait(textHas('基本信息'));
  check(errors.length === 0, 'no uncaught runtime errors');
  writeFileSync(join(out, 'browser.json'), JSON.stringify({ checks, errors, apiRequests: network.length }, null, 2));
  console.log(JSON.stringify({ checks, errors, apiRequests: network.length }));
} catch (error) {
  if (send) { try { const shot = await send('Page.captureScreenshot', { format: 'png' }); writeFileSync(join(out, 'failure.png'), Buffer.from(shot.data, 'base64')); } catch {} }
  throw error;
} finally {
  ws?.close(); chrome.kill(); await pause(500);
  // Resolve and verify the temporary target remains under tmpdir before deleting it.
  if (!relative(tmpdir(), profile).startsWith('..')) { try { rmSync(profile, { recursive: true, force: true }); } catch {} }
}
