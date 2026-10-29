import dayjs from 'dayjs';
import type { PlanTask } from '@/types/plan';

export type GanttEditOperation = 'move' | 'resize-start' | 'resize-end' | 'progress';
export type GanttEditPhase = 'locked' | 'ready' | 'dragging' | 'confirming' | 'saving' | 'refreshing';
export interface GanttEditDraft {
  task: PlanTask;
  rowId: string;
  operation: GanttEditOperation;
  viewKey: string;
  start: string;
  end: string;
  progress: number;
}

export const PROGRESS_MIN_WIDTH = 72;
export const DATE_HANDLE_WIDTH = 12;
export const DATE_HANDLE_GAP = 4;
export const PROGRESS_HANDLE_WIDTH = 16;
export const DRAG_THRESHOLD = 5;

export function canEditProgress(task: PlanTask): boolean {
  return task.can_edit && !task.has_children && task.status !== 'cancelled';
}

export function makeGanttDraft(task: PlanTask, rowId: string, operation: GanttEditOperation, viewKey: string): GanttEditDraft {
  return { task, rowId, operation, viewKey, start: task.start_date, end: task.end_date, progress: task.progress };
}

export function validNaturalDate(value: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) && dayjs(value).isValid() && dayjs(value).format('YYYY-MM-DD') === value;
}

export function validGanttDraft(draft: GanttEditDraft): boolean {
  return draft.operation === 'progress'
    ? Number.isInteger(draft.progress) && draft.progress >= 0 && draft.progress <= 100
    : validNaturalDate(draft.start) && validNaturalDate(draft.end) && draft.start <= draft.end;
}

export function changedGanttDraft(draft: GanttEditDraft): boolean {
  return draft.operation === 'progress'
    ? draft.progress !== draft.task.progress
    : draft.start !== draft.task.start_date || draft.end !== draft.task.end_date;
}

/** Round half a day away from zero in both directions. */
export function roundDayDelta(value: number): number {
  return Math.sign(value) * Math.round(Math.abs(value));
}

export function taskBarWidth(task: PlanTask, dayWidth: number): number {
  return Math.max(1, dayjs(task.end_date).diff(dayjs(task.start_date), 'day') + 1) * dayWidth;
}

/** Always derive from the captured task, never from the preceding pointermove. */
export function offsetGanttDraft(draft: GanttEditDraft, deltaX: number, dayWidth: number): GanttEditDraft {
  const task = draft.task;
  if (draft.operation === 'progress') {
    return { ...draft, progress: Math.max(0, Math.min(100, Math.round(task.progress + 100 * deltaX / taskBarWidth(task, dayWidth)))) };
  }
  const days = roundDayDelta(deltaX / dayWidth);
  const start = dayjs(task.start_date).add(days, 'day').format('YYYY-MM-DD');
  const end = dayjs(task.end_date).add(days, 'day').format('YYYY-MM-DD');
  if (draft.operation === 'resize-start') return { ...draft, start: start > task.end_date ? task.end_date : start };
  if (draft.operation === 'resize-end') return { ...draft, end: end < task.start_date ? task.start_date : end };
  return { ...draft, start, end };
}

export function progressHandleLeft(width: number, progress: number): number {
  return Math.max(0, Math.min(width - PROGRESS_HANDLE_WIDTH, width * progress / 100 - PROGRESS_HANDLE_WIDTH / 2));
}

/** Coordinates are relative to the timeline; the sticky label is outside this viewport. */
export function ganttEditHandles(left: number, width: number, progress: number, viewportStart: number, viewportEnd: number) {
  const fits = (a: number, b: number) => a >= viewportStart && b <= viewportEnd;
  const handleLeft = progressHandleLeft(width, progress);
  return {
    start: fits(left - DATE_HANDLE_GAP - DATE_HANDLE_WIDTH, left),
    end: fits(left + width, left + width + DATE_HANDLE_GAP + DATE_HANDLE_WIDTH),
    progress: Math.min(left + width, viewportEnd) - Math.max(left, viewportStart) >= PROGRESS_MIN_WIDTH &&
      fits(left + handleLeft, left + handleLeft + PROGRESS_HANDLE_WIDTH),
    progressLeft: handleLeft,
  };
}

export function progressStatus(progress: number): string {
  return progress === 0 ? '未开始' : progress === 100 ? '已完成' : '进行中';
}
