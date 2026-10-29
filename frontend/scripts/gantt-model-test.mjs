// Node 24 native type stripping; no additional test runtime dependency.
import assert from 'node:assert/strict';
import * as m from '../src/components/gantt/ganttEditModel.ts';
let checks = 0;
const eq = (actual, expected) => { assert.deepEqual(actual, expected); checks++; };
const task = { id: 'leaf', can_edit: true, has_children: false, status: 'in_progress', progress: 50,
  start_date: '2028-02-28', end_date: '2028-03-01', updated_at: '2026-10-29T01:00:00.123456Z' };
const draft = op => m.makeGanttDraft(task, 'row', op, 'view');
eq(m.roundDayDelta(-0.5), -1); eq(m.roundDayDelta(0.5), 1);
eq(m.offsetGanttDraft(draft('move'), 34, 34).start, '2028-02-29');
eq(m.offsetGanttDraft(draft('move'), -34, 34).end, '2028-02-29');
eq(m.offsetGanttDraft(draft('resize-start'), 500, 34).start, task.end_date);
eq(m.offsetGanttDraft(draft('resize-start'), 34, 34).end, task.end_date);
eq(m.offsetGanttDraft(draft('resize-end'), -500, 34).end, task.start_date);
eq(m.offsetGanttDraft(draft('resize-end'), 34, 34).start, task.start_date);
eq(m.taskBarWidth(task, 34), 102);
eq(m.offsetGanttDraft(draft('progress'), 51, 34).progress, 100);
eq(m.offsetGanttDraft(draft('progress'), -51, 34).progress, 0);
eq(m.offsetGanttDraft(draft('progress'), 0, 34).progress, 50);
eq(m.offsetGanttDraft(draft('progress'), 20, 34).start, task.start_date);
for (const progress of [0, 1, 50, 99, 100]) {
  const left = m.progressHandleLeft(72, progress);
  eq(left >= 0 && left + m.PROGRESS_HANDLE_WIDTH <= 72, true);
  eq(m.ganttEditHandles(30, 72, progress, 0, 200).progress, true);
}
for (const width of [71, 72, 73]) eq(m.ganttEditHandles(30, width, 50, 0, 200).progress, width >= 72);
eq(m.ganttEditHandles(30, 200, 0, 100, 300).progress, false);
eq(m.ganttEditHandles(30, 200, 100, 0, 200).progress, false);
eq(m.ganttEditHandles(30, 200, 50, 100, 200).progress, true);
eq(m.ganttEditHandles(30, 200, 50, 31, 220).start, false);
eq(m.ganttEditHandles(30, 200, 50, 0, 240).end, false);
eq(m.changedGanttDraft(draft('move')), false);
eq(m.changedGanttDraft({ ...draft('progress'), progress: 51 }), true);
for (const progress of [0, 1, 99, 100]) eq(m.validGanttDraft({ ...draft('progress'), progress }), true);
for (const progress of [-1, 101, 1.5, NaN, Infinity]) eq(m.validGanttDraft({ ...draft('progress'), progress }), false);
for (const value of ['2028-02-29', '2026-12-31']) eq(m.validNaturalDate(value), true);
for (const value of ['2026-02-29', '2026-02-31', '2026-13-01', '', '2026-1-01']) eq(m.validNaturalDate(value), false);
eq(m.validGanttDraft({ ...draft('move'), end: '2028-02-27' }), false);
eq(m.canEditProgress(task), true);
for (const patch of [{ has_children: true }, { can_edit: false }, { status: 'cancelled' }]) eq(m.canEditProgress({ ...task, ...patch }), false);
eq(m.canEditProgress({ ...task, status: 'completed', progress: 100 }), true);
console.log(`P03 model: ${checks} checks passed`);
