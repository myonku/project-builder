// Companion to gantt-api-fixture.mjs, operates only on its local isolated fixtures.
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
const fixture = JSON.parse(readFileSync('.verify/p03/fixture.json', 'utf8'));
const origin = process.env.P03_TEST_API || 'http://127.0.0.1:8013';
assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/);
let cookie;
async function api(path, method = 'GET', body) {
  const r = await fetch(origin + '/api/v1' + path, { method, headers: { 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (r.headers.get('set-cookie')) cookie = r.headers.get('set-cookie').split(';')[0];
  const payload = await r.json(); assert.ok(r.ok, JSON.stringify(payload)); return payload.data;
}
await api('/auth/login', 'POST', { identifier: process.env.PREVIEW_TEST_USER || 'bootstrap_admin', password: process.env.PREVIEW_TEST_PASSWORD || 'bootstrap_admin_test_password' });
const [operation = 'state', argument] = process.argv.slice(2);
if (operation === 'progress') {
  const current = await api('/tasks/' + fixture.long.id);
  const changed = await api('/tasks/' + current.id, 'PATCH', { progress: Number(argument), expected_updated_at: current.updated_at });
  console.log(JSON.stringify({ id: changed.id, progress: changed.progress, updated_at: changed.updated_at }));
} else if (operation === 'members') {
  const member = await api('/users', 'POST', { username: 'p03_member_' + Date.now(), email: `p03_${Date.now()}@test.local`, display_name: 'P03 第二成员', password: 'p03_local_test_password' });
  await api('/groups/' + fixture.group.id + '/members', 'POST', { user_id: member.id, role: 'member' });
  await api('/projects/' + fixture.project.id + '/members', 'POST', { user_id: member.id });
  await api('/tasks/' + fixture.long.id + '/members', 'POST', { user_id: member.id });
  fixture.member = { id: member.id, username: member.username };
  writeFileSync('.verify/p03/fixture.json', JSON.stringify(fixture, null, 2)); console.log(JSON.stringify(fixture.member));
} else if (operation === 'share') {
  const current = await api('/projects/' + fixture.project.id);
  await api('/projects/' + current.id, 'PATCH', { visibility: 'group_editable', expected_updated_at: current.updated_at });
  console.log('Project shared with group');
} else if (operation === 'archive') {
  const current = await api('/projects/' + fixture.project.id);
  const archived = await api('/projects/' + current.id + '/archive', 'POST', { expected_updated_at: current.updated_at });
  console.log(JSON.stringify({ status: archived.status }));
} else if (operation === 'state') {
  const task = await api('/tasks/' + fixture.long.id);
  const graph = await api('/plan-views/projects/' + fixture.project.id + '/gantt');
  const reminders = await api('/reminders?task_id=' + task.id + '&page_size=100');
  const state = { task: { start: task.start_date, end: task.end_date, progress: task.progress, status: task.status, version: task.updated_at },
    parent: graph.tasks.find(t => t.id === fixture.parent.id)?.progress, project: graph.project.progress,
    summary: graph.summary, reminders: reminders.map(r => ({ kind: r.kind, status: r.status, cycle: r.cycle })) };
  writeFileSync('.verify/p03/api-state.json', JSON.stringify(state, null, 2)); console.log(JSON.stringify(state));
} else throw new Error('Unknown operation');
