// Production-build browser tests against an isolated in-memory API fixture (no real database).
// Run: pnpm build; node scripts/statistics-smoke.mjs
// Optional CHROME_PATH overrides the installed Chrome executable.
import assert from 'node:assert/strict';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { resolve, join, extname, relative } from 'node:path';
import { tmpdir } from 'node:os';
const out = resolve('.verify/p04'); mkdirSync(out, { recursive: true });
const uuid = n => `0199f0c2-8b1e-7a3c-9d2f-${String(n).padStart(12, '0')}`;
const groupA = uuid(1), groupB = uuid(2), denied = uuid(3), largeGroup = uuid(4), emptyGroup = uuid(5);
const uid = uuid(101), other = uuid(102), pid = uuid(201);
const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());
const date = delta => new Date(Date.parse(today + 'T00:00:00Z') + delta * 86400000).toISOString().slice(0, 10);
const now = new Date().toISOString();
const user = { id: uid, username: 'fixture', display_name: '统计验收员', email: 'fixture@example.test', timezone: 'Asia/Shanghai', system_role: 'admin', status: 'active', avatar_updated_at: null, created_at: now, updated_at: now };
const person = (id, name) => ({ user_id: id, username: id === uid ? 'fixture' : 'member', display_name: name, role: 'member', avatar_updated_at: null });
const project = (id, name) => ({ id, name, description: null, group_id: groupA, status: 'in_progress', visibility: 'group_editable', start_date: date(-20), end_date: date(40), color: null, archived_at: null, is_archived: false, is_read_only: false, owner_id: uid, my_role: 'owner', membership_role: 'owner', has_tasks: true, task_count: 16, progress: 63, overdue_task_count: 1, created_at: now, updated_at: now });
const projects = Array.from({ length: 10 }, (_, i) => project(i ? uuid(201 + i) : pid, `共享项目 ${i + 1}`));
const makeTask = (i, extra = {}) => ({ id: uuid(301 + i), project_id: pid, parent_id: null, has_children: false, name: `可见任务 ${i + 1}`, description: null, status: 'in_progress', progress: 40, priority: 1, start_date: date(-10), end_date: date(i), is_shared: true, is_overdue: false, is_milestone: false, can_edit: true, owner: person(uid, '甲成员'), members: [person(uid, '甲成员')], updated_by: uid, updated_by_name: '甲成员', updated_at: now, deleted_at: null, ...extra });
const tasks = Array.from({ length: 16 }, (_, i) => makeTask(i));
tasks[0] = makeTask(0, { has_children: true, name: '父任务不计入叶子' });
tasks[1] = makeTask(1, { end_date: date(-1), is_overdue: true, members: [person(uid, '甲成员'), person(other, '乙成员')], parent_id: tasks[0].id });
tasks[2] = makeTask(2, { status: 'completed', progress: 20 });
tasks[3] = makeTask(3, { status: 'cancelled', members: [] });
tasks[4] = makeTask(4, { end_date: today, status: 'not_started' });
tasks[5] = makeTask(5, { members: [] });
const largeTasks = Array.from({ length: 1000 }, (_, i) => makeTask(i + 1000, { project_id: projects[i % 10].id,
  members: [person(uuid(10000 + i % 100), `成员 ${i % 100}`)] }));
const page = data => ({ data, pagination: { page: 1, page_size: 100, total: data.length, has_next: false } });
const summary = ts => ({ project_count: projects.length, task_count: ts.length, in_progress_task_count: ts.filter(t => t.status === 'in_progress').length, overdue_task_count: ts.filter(t => t.is_overdue).length, overall_progress: 63 });
let slowA = false, failA = false;
const requests = [], unknown = [];
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const path = url.pathname;
  const json = (data, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
  if (path.startsWith('/api/')) {
    requests.push({ path, query: url.search, method: req.method });
    if (path.endsWith('/auth/me')) return json({ data: user });
    if (path.endsWith('/groups/query')) {
      let body = ''; for await (const chunk of req) body += chunk;
      const number = JSON.parse(body).page;
      const rows = (number === 1 ? [[groupA, '验收 A 组'], [groupB, '验收 B 组'], [denied, '拒绝组'], [largeGroup, '大数据组'], [emptyGroup, '空组']] : [[uuid(6), '第二页工作组']])
        .map(([id, name]) => ({ id, name, membership_role: 'owner', membership_status: 'active' }));
      return json({ data: rows, pagination: { page: number, page_size: 100, total: 6, has_next: number === 1 } });
    }
    if (path.includes('/plan-views/groups/')) {
      const id = path.split('/').at(-1);
      if (id === groupA && slowA) await new Promise(r => setTimeout(r, 700));
      if (id === denied) return json({ error: { code: 'group_not_found', message: 'Group not found' } }, 404);
      if (id === groupA && failA) return json({ error: { code: 'internal_error', message: 'fixture error' } }, 500);
      const ts = id === groupB ? [makeTask(99, { name: 'B 组独有任务' })] : id === emptyGroup ? [] : id === largeGroup ? largeTasks : tasks;
      return json({ data: { group_id: id, projects: id === emptyGroup ? [] : projects, tasks: ts, summary: summary(ts) } });
    }
    if (path.endsWith('/plan-views/personal')) return json({ data: { group_id: null, projects, tasks, summary: summary(tasks) } });
    if (path.endsWith('/gantt')) return json({ data: { project: projects[0], tasks, summary: summary(tasks), can_manage_project: true, can_edit_tasks: true } });
    if (path.endsWith('/projects/query')) return json(page(projects));
    if (path === `/api/v1/projects/${pid}`) return json({ data: projects[0] });
    if (path.endsWith('/tasks/query')) return json({ ...page([tasks[0]]), pagination: { page: 1, page_size: 20, total: tasks.length, has_next: false } });
    if (path.match(/\/tasks\/[^/]+$/)) return json({ data: tasks.find(t => path.endsWith(t.id)) ?? tasks[1] });
    if (path.includes('/members/query') || path.includes('/attachments/query') || path.includes('/reminders/query')) return json(page([]));
    unknown.push(path); return json(page([]));
  }
  const file = resolve('dist', '.' + path);
  const root = resolve('dist');
  if (relative(root, file).startsWith('..')) { res.writeHead(403); return res.end(); }
  const target = existsSync(file) && extname(file) ? file : join(root, 'index.html');
  const mime = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.woff2': 'font/woff2', '.woff': 'font/woff' };
  res.writeHead(200, { 'Content-Type': mime[extname(target)] ?? 'application/octet-stream' }); res.end(readFileSync(target));
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const origin = `http://127.0.0.1:${server.address().port}`;
const profile = mkdtempSync(join(tmpdir(), 'worklane-p04-'));
const executable = process.env.CHROME_PATH || 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const chrome = spawn(executable, ['--headless=new', '--no-first-run', '--disable-gpu', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { windowsHide: true, stdio: 'ignore' });
let ws; let checks = 0; const errors = [];
const pause = ms => new Promise(r => setTimeout(r, ms));
const check = (value, label) => { assert.ok(value, label); checks++; };
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
    if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') errors.push(JSON.stringify(msg.params.args));
  };
  await new Promise((r, reject) => { ws.onopen = r; ws.onerror = reject; });
  const send = (method, params = {}) => new Promise((resolve, reject) => { const id = nextId++; pending.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params })); });
  const evaluate = async expression => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const wait = async expression => { for (let i = 0; i < 150; i++) { if (await evaluate(expression)) return; await pause(50); } throw new Error('Timed out: ' + expression); };
  const body = () => evaluate('document.body.innerText');
  const click = async text => { check(await evaluate(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent.includes(${JSON.stringify(text)}) && !b.disabled); if(!b)return false; b.click(); return true})()`), 'button ' + text); await pause(100); };
  const go = async path => { await send('Page.navigate', { url: origin + path }); await wait('!!document.querySelector("main")'); };
  const navigate = async path => { await evaluate(`history.pushState({}, '', ${JSON.stringify(path)}); dispatchEvent(new PopStateEvent('popstate'))`); await pause(100); };
  const ready = () => wait('document.body.innerText.includes("以上为组级范围")');
  const screenshot = async name => { const r = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); writeFileSync(join(out, name + '.png'), Buffer.from(r.data, 'base64')); };
  await send('Runtime.enable'); await send('Page.enable');
  const themeScript = await send('Page.addScriptToEvaluateOnNewDocument', { source: 'localStorage.setItem("mui-mode", "light")' });
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  await go(`/statistics?groupId=${groupA}`); await ready();
  check((await body()).includes('数据统计'), 'statistics route');
  check(requests.filter(r => r.path.endsWith('/groups/query')).length === 2, 'group options read all pages');
  check((await body()).includes('共 15 个叶子任务'), 'parent excluded');
  check((await body()).includes('显示 8 / 共 10 个项目'), 'project limit labeled');
  check(!(await body()).includes('私有任务'), 'no fabricated hidden data');
  await screenshot('group-desktop');
  await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: themeScript.identifier });
  await click('查看完整明细'); check((await body()).includes('共 15 条'), 'full leaf detail count');
  await click('进行中'); check((await body()).includes('任务明细 · 进行中'), 'chart selection');
  await click('清除图形选择');
  await send('Page.bringToFront');
  await evaluate('[...document.querySelectorAll("button")].find(b=>b.textContent.includes("已完成") && !b.disabled).focus()');
  check(await evaluate('document.activeElement.textContent.includes("已完成")'), 'status button keyboard focus');
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13, text: '\r' });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
  await wait('document.body.innerText.includes("任务明细 · 已完成")'); checks++;
  await click('成员分布'); await pause(150);
  check((await body()).includes('甲成员'), 'member list');
  check((await body()).includes('合计不等于组级任务数'), 'multi-member explanation');
  await click('甲成员'); check((await body()).includes('参与任务详情'), 'member selected');
  check((await evaluate('location.search')).includes('memberId='), 'member in URL');
  await screenshot('members-desktop');
  await evaluate('window.__copied=""; Object.defineProperty(navigator.clipboard,"writeText",{value:async text=>{window.__copied=text},configurable:true})');
  await click('复制摘要');
  check((await evaluate('window.__copied')).includes('成员：甲成员'), 'copy member scope');
  check((await evaluate('window.__copied')).includes('Asia/Shanghai'), 'copy timezone');
  check((await evaluate('window.__copied')).includes('项目当前进度'), 'copy project progress');
  await send('Emulation.setEmulatedMedia', { media: 'print' });
  check(await evaluate('getComputedStyle(document.querySelector(".statistics-no-print")).display === "none"'), 'print controls hidden');
  check(await evaluate('getComputedStyle(document.querySelector(".dashboard-layout")).overflow === "visible"'), 'print root does not clip pages');
  check(await evaluate('[...document.querySelectorAll(".MuiDrawer-root")].every(e=>getComputedStyle(e).display === "none")'), 'print sidebar hidden');
  const pdf = await send('Page.printToPDF', { printBackground: true, preferCSSPageSize: true }); writeFileSync(join(out, 'report.pdf'), Buffer.from(pdf.data, 'base64')); checks++;
  await send('Emulation.setEmulatedMedia', { media: '' });
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await pause(150); check(await evaluate('document.documentElement.scrollWidth <= innerWidth'), 'mobile no horizontal overflow');
  await screenshot('members-mobile');
  await evaluate('document.querySelector("main").scrollTo(0,1000)'); await pause(100); await screenshot('members-mobile-chart');
  await evaluate('localStorage.setItem("mui-mode", "dark")');
  await go(`/statistics?groupId=${groupA}&tab=members`); await ready(); await screenshot('members-mobile-dark');
  await evaluate('document.querySelector("main").scrollTo(0,1000)'); await pause(100); await screenshot('members-mobile-dark-chart');
  await navigate(`/statistics?groupId=${groupA}&dueFrom=2099-01-01`); await ready();
  check((await body()).includes('当前筛选无结果'), 'empty date filter');
  await navigate(`/statistics?groupId=${groupA}&dueFrom=2026-12-01&dueTo=2026-01-01`);
  await wait('document.body.innerText.includes("查询条件无效")');
  check(await evaluate('[...document.querySelectorAll("button")].find(b=>b.textContent==="复制摘要").disabled'), 'invalid scope cannot report');
  await navigate(`/statistics?groupId=${denied}`); await wait('document.body.innerText.includes("统计范围不可用")');
  check(!(await body()).includes('共 15 个叶子任务'), 'denied scope cleared');
  slowA = true;
  await navigate(`/statistics?groupId=${groupA}`); await pause(50);
  await navigate(`/statistics?groupId=${groupB}`); await ready(); await pause(800);
  check((await body()).includes('B 组独有任务'), 'latest scope wins');
  check(!(await body()).includes('父任务不计入叶子'), 'late A response ignored'); slowA = false;
  await navigate(`/statistics?groupId=${emptyGroup}`); await ready();
  await evaluate('history.back()'); await pause(150); await ready();
  check((await body()).includes('B 组独有任务'), 'back restores group scope');
  const beforeArchive = requests.length;
  await navigate(`/statistics?groupId=${groupB}&includeArchived=true`); await ready();
  check(requests.slice(beforeArchive).some(r => r.query.includes('include_archived=true')), 'archive toggle reaches API');
  await navigate(`/statistics?groupId=${groupA}`); await ready(); failA = true;
  await click('刷新'); await wait('document.body.innerText.includes("统计范围不可用")');
  check(!(await body()).includes('共 15 个叶子任务'), 'failed refresh does not show stale statistics'); failA = false;
  await navigate(`/statistics?groupId=${emptyGroup}`); await ready(); check((await body()).includes('当前没有未完成的叶子任务'), 'true empty scope');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  const beforeHome = requests.length; await go('/'); await wait('document.body.innerText.includes("参与项目的叶子任务状态")');
  check(requests.slice(beforeHome).filter(r => r.path.endsWith('/plan-views/personal')).length === 1, 'dashboard one personal read');
  await screenshot('dashboard');
  const beforeProject = requests.length; await go(`/projects/${pid}`); await wait('document.body.innerText.includes("共 15 个叶子任务")');
  check(requests.slice(beforeProject).filter(r => r.path.endsWith('/gantt')).length === 1, 'project one full projection read');
  check((await body()).includes('父任务不计入叶子'), 'project table displays parent while chart counts leaves');
  await screenshot('project');
  await go(`/gantt?view=projects&projectId=${pid}`); await wait('document.body.innerText.includes("允许编辑")');
  check((await body()).includes('进度视图'), 'old gantt route preserved'); checks++;
  const largeStart = performance.now(); await go(`/statistics?groupId=${largeGroup}`); await ready();
  check((await body()).includes('共 1000 个叶子任务'), 'large fixture rendered');
  await click('成员分布'); await pause(150);
  check((await body()).includes('显示 10 / 共 100 位匹配成员'), 'large member truncation labeled');
  const renderAfterResponseMs = await evaluate('performance.now() - performance.getEntriesByType("resource").find(e=>e.name.includes("plan-views/groups")).responseEnd');
  await screenshot('large-members');
  check(errors.length === 0, 'no browser runtime/console errors: ' + errors.join('\n'));
  check(unknown.length === 0, 'all fixture API routes defined: ' + unknown.join(','));
  const result = { checks, origin, fixtureOnly: true, errors, requests: requests.length, benchmark: { tasks: 1000, members: 100, fullNavigationMs: Math.round(performance.now() - largeStart), renderAndMemberInteractionAfterResponseMs: Math.round(renderAfterResponseMs) }, artifacts: out };
  writeFileSync(join(out, 'browser-result.json'), JSON.stringify(result, null, 2)); console.log(JSON.stringify(result));
} finally {
  ws?.close(); chrome.kill(); server.closeAllConnections(); server.close();
  await pause(500);
  // Delete only this freshly created temporary Chrome profile.
  if (!relative(tmpdir(), profile).startsWith('..') && profile.includes('worklane-p04-')) {
    try { rmSync(profile, { recursive: true, force: true }); } catch { /* Chrome may still hold a temporary file on Windows. */ }
  }
}
