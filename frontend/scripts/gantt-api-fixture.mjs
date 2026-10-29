// Creates new isolated fixtures only. Never run against a production API.
// node scripts/gantt-api-fixture.mjs http://127.0.0.1:8013
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
const origin = process.argv[2] || 'http://127.0.0.1:8013';
assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/);
let cookie;
async function api(path, method = 'GET', body) {
  const r = await fetch(origin + '/api/v1' + path, { method, headers: { 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (r.headers.get('set-cookie')) cookie = r.headers.get('set-cookie').split(';')[0];
  const payload = await r.json(); assert.ok(r.ok, JSON.stringify(payload)); return payload.data;
}
await api('/auth/login', 'POST', { identifier: process.env.PREVIEW_TEST_USER || 'bootstrap_admin', password: process.env.PREVIEW_TEST_PASSWORD || 'bootstrap_admin_test_password' });
const group = await api('/groups', 'POST', { name: 'P03 验收 ' + Date.now() });
const project = await api('/projects', 'POST', { name: 'P03 甘特图编辑验收', group_id: group.id, visibility: 'group_editable', start_date: '2026-10-01', end_date: '2026-12-31' });
const make = (name, start_date, end_date, extra = {}) => api('/projects/' + project.id + '/tasks', 'POST', { name, start_date, end_date, is_shared: true, ...extra });
const parent = await make('父任务（汇总只读）', '2026-10-20', '2026-11-15');
const long = await make('长任务（拖拽验收）', '2026-10-26', '2026-11-02', { parent_id: parent.id });
await api('/tasks/' + long.id, 'PATCH', { progress: 50, expected_updated_at: long.updated_at });
const short = await make('一天短任务', '2026-10-29', '2026-10-29');
const completed = await make('已完成任务', '2026-10-25', '2026-11-04');
await api('/tasks/' + completed.id, 'PATCH', { progress: 100, expected_updated_at: completed.updated_at });
const cancelled = await make('已取消任务', '2026-10-25', '2026-11-04');
await api('/tasks/' + cancelled.id, 'PATCH', { status: 'cancelled', expected_updated_at: cancelled.updated_at });
const milestone = await make('多日里程碑', '2026-10-28', '2026-10-30', { is_milestone: true });
const fixture = { group, project, parent, long: await api('/tasks/' + long.id), short, completed, cancelled, milestone };
mkdirSync('.verify/p03', { recursive: true }); writeFileSync('.verify/p03/fixture.json', JSON.stringify(fixture, null, 2));
console.log(JSON.stringify({ groupId: group.id, projectId: project.id, tasks: [parent.id, long.id, short.id, completed.id, cancelled.id, milestone.id] }));
