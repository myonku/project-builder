import * as React from 'react';
import { updateTask, updateTaskDates } from '@/api/tasks';
import { toApiError } from '@/api/client';
import { describeApiError, useApiError } from './useApiError';
import { canEditProgress, changedGanttDraft, makeGanttDraft, validGanttDraft,
  type GanttEditDraft, type GanttEditOperation, type GanttEditPhase } from '@/components/gantt/ganttEditModel';
import type { PlanTask } from '@/types/plan';

export interface GanttEditController {
  phase: GanttEditPhase;
  draft: GanttEditDraft | null;
  enabled: boolean;
  busy: boolean;
  message: string;
  error: string;
  toggle: () => void;
  lock: () => void;
  begin: (task: PlanTask, rowId: string, operation: GanttEditOperation, source: HTMLElement, direct?: boolean) => GanttEditDraft | null;
  preview: (draft: GanttEditDraft) => void;
  finish: (draft: GanttEditDraft) => void;
  cancel: () => void;
  change: (patch: Partial<Pick<GanttEditDraft, 'start' | 'end' | 'progress'>>) => void;
  save: () => Promise<void>;
}

export function useGanttEdit(viewKey: string, available: boolean, reload: () => Promise<boolean>): GanttEditController {
  const [phase, setPhase] = React.useState<GanttEditPhase>('locked');
  const [draft, setDraft] = React.useState<GanttEditDraft | null>(null);
  const [message, setMessage] = React.useState('');
  const [error, setError] = React.useState('');
  // Synchronous guards protect pointerdown/double submit before React commits.
  const state = React.useRef({ phase, draft });
  const identity = React.useRef(viewKey);
  identity.current = viewKey;
  const alive = React.useRef(true);
  const source = React.useRef<HTMLElement | null>(null);
  const sourceRow = React.useRef('');
  const session = React.useRef(0);
  const { handle } = useApiError();
  const transition = React.useCallback((next: GanttEditPhase, value: GanttEditDraft | null = null) => {
    state.current = { phase: next, draft: value }; setDraft(value); setPhase(next);
  }, []);
  const focus = React.useCallback(() => {
    const element = source.current;
    requestAnimationFrame(() => {
      if (element?.isConnected) element.focus();
      else {
        const row = [...document.querySelectorAll<HTMLElement>('[data-gantt-row]')].find(node => node.dataset.ganttRow === sourceRow.current);
        const fallback = row?.querySelector<HTMLElement>('[role="button"][tabindex="0"], button:not(:disabled)') ?? document.querySelector<HTMLElement>('[data-gantt-edit-toggle]');
        fallback?.focus();
      }
    });
  }, []);
  const lock = React.useCallback(() => {
    session.current++; transition('locked'); setError('');
  }, [transition]);
  React.useEffect(() => { lock(); setMessage(''); }, [viewKey, lock]);
  React.useEffect(() => {
    if (!available && !['saving', 'refreshing', 'locked'].includes(state.current.phase)) lock();
  }, [available, lock]);
  React.useEffect(() => { alive.current = true; return () => { alive.current = false; session.current++; }; }, []);
  const cancel = React.useCallback(() => {
    if (!['dragging', 'confirming'].includes(state.current.phase)) return;
    transition('ready'); setError(''); focus();
  }, [transition, focus]);
  React.useEffect(() => {
    const abortPreview = () => { if (state.current.phase === 'dragging') cancel(); };
    const hidden = () => { if (document.hidden) abortPreview(); };
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && state.current.phase === 'dragging') { event.preventDefault(); abortPreview(); }
    };
    window.addEventListener('blur', abortPreview);
    document.addEventListener('visibilitychange', hidden);
    document.addEventListener('keydown', escape);
    return () => {
      window.removeEventListener('blur', abortPreview);
      document.removeEventListener('visibilitychange', hidden);
      document.removeEventListener('keydown', escape);
    };
  }, [cancel]);
  const begin: GanttEditController['begin'] = (task, rowId, operation, element, direct = false) => {
    if (!available || state.current.phase !== 'ready' || !task.can_edit || (operation === 'progress' && !canEditProgress(task))) return null;
    source.current = element;
    sourceRow.current = rowId;
    const value = makeGanttDraft(task, rowId, operation, viewKey);
    session.current++; setError(''); transition(direct ? 'confirming' : 'dragging', value);
    return value;
  };
  const preview = (value: GanttEditDraft) => {
    if (state.current.phase === 'dragging' && value.viewKey === identity.current && state.current.draft?.task.id === value.task.id) transition('dragging', value);
  };
  const finish = (value: GanttEditDraft) => {
    if (state.current.phase !== 'dragging' || value.viewKey !== identity.current || state.current.draft?.rowId !== value.rowId || state.current.draft.task.id !== value.task.id) return;
    if (!validGanttDraft(value) || !changedGanttDraft(value)) { cancel(); return; }
    transition('confirming', value);
  };
  const save = async () => {
    const current = state.current;
    const value = current.draft;
    if (current.phase !== 'confirming' || !value || value.viewKey !== identity.current || !validGanttDraft(value) || !changedGanttDraft(value)) return;
    const id = session.current;
    const active = () => alive.current && session.current === id && identity.current === value.viewKey;
    transition('saving', value); setError('');
    try {
      const signal = AbortSignal.timeout(30_000);
      if (value.operation === 'progress') await updateTask(value.task.id, { progress: value.progress, expected_updated_at: value.task.updated_at }, signal);
      else await updateTaskDates(value.task.id, { start_date: value.start, end_date: value.end, expected_updated_at: value.task.updated_at }, signal);
    } catch (caught) {
      if (!active()) return;
      const apiError = toApiError(caught);
      handle(apiError, { silent: true });
      if ([400, 422, 429].includes(apiError.status)) {
        setError([describeApiError(apiError), ...Object.values(apiError.fields)].join(' '));
        transition('confirming', value); return;
      }
      const uncertain = apiError.status === 0 || apiError.status >= 500 || apiError.code === 'invalid_response';
      transition('locked');
      setMessage(uncertain ? '保存结果未确认，请使用刷新核对当前数据；不会自动重试。' : apiError.isVersionConflict ? '任务已变化，请刷新后重新编辑。' : describeApiError(apiError));
      // Mark the graph unverified even if a failing read leaves old data in memory.
      verified.current = false;
      if (!uncertain && !apiError.isUnauthorized) await reload();
      if (active()) focus(); return;
    }
    if (!active()) return;
    transition('refreshing', value); setMessage('已保存，正在刷新视图…');
    const ok = await reload();
    if (!active()) return;
    verified.current = ok;
    setMessage(ok ? '修改已保存，视图已更新。' : '已保存，视图刷新失败。请刷新核对后重新开启编辑。');
    transition(ok ? 'ready' : 'locked'); focus();
  };
  const verified = React.useRef(true);
  // A completed external read enables a fresh session; loading disables the switch.
  React.useEffect(() => {
    if (!available) return;
    if (!verified.current) setMessage((previous) => previous.includes('保存结果未确认') || previous.includes('先刷新核对')
      ? '当前数据已刷新，请核对后重新开启编辑；此前请求的执行归属仍未确认。'
      : previous.includes('已保存，视图刷新失败') ? '已保存，视图已刷新，可重新开启编辑。' : previous);
    verified.current = true;
  }, [available, viewKey]);
  return {
    phase, draft, message, error, enabled: phase !== 'locked', busy: phase !== 'locked' && phase !== 'ready',
    lock, begin, preview, finish, cancel, save,
    toggle: () => {
      if (state.current.phase === 'ready') lock();
      else if (state.current.phase === 'locked' && available && verified.current) { setMessage(''); transition('ready'); }
      else if (!verified.current) setMessage('请先刷新核对当前数据，再重新开启编辑。');
    },
    change: (patch) => { const current = state.current; if (current.phase === 'confirming' && current.draft) { setError(''); transition('confirming', { ...current.draft, ...patch }); } },
  };
}
