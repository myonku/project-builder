import * as React from 'react';
import dayjs from 'dayjs';
import Box from '@mui/material/Box';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import { useTheme } from '@mui/material/styles';
import type { PlanTask } from '@/types/plan';
import type { GanttEditController } from '@/hooks/useGanttEdit';
import { barGeometry, type GanttTimeWindow } from './ganttModel';
import { canEditProgress, DATE_HANDLE_GAP, DATE_HANDLE_WIDTH, DRAG_THRESHOLD, ganttEditHandles,
  offsetGanttDraft, progressHandleLeft, PROGRESS_HANDLE_WIDTH, taskBarWidth,
  type GanttEditDraft, type GanttEditOperation } from './ganttEditModel';

interface PointerSession {
  original: GanttEditDraft;
  latest: GanttEditDraft;
  x: number;
  scroll: number;
  pointer: number;
  moved: boolean;
  element: HTMLElement;
  handles: ReturnType<typeof ganttEditHandles>;
}

export interface GanttTaskBarProps {
  task: PlanTask;
  rowId: string;
  window: GanttTimeWindow;
  active: boolean;
  onOpenTask: (id: string) => void;
  edit: GanttEditController;
  scrollRef: React.RefObject<HTMLDivElement | null>;
  viewport: { start: number; end: number };
  finePointer: boolean;
}

export default function GanttTaskBar({ task, rowId, window: timeline, active, onOpenTask, edit, scrollRef, viewport, finePointer }: GanttTaskBarProps) {
  const theme = useTheme();
  const pointer = React.useRef<PointerSession | null>(null);
  const keyboard = React.useRef<GanttEditDraft | null>(null);
  const suppressClick = React.useRef(0);
  const latestEdit = React.useRef(edit); latestEdit.current = edit;
  const ownPreview = edit.draft?.task.id === task.id ? edit.draft : null;
  const displayTask = ownPreview ? { ...task, start_date: ownPreview.start, end_date: ownPreview.end, progress: ownPreview.progress } : task;
  const geometry = barGeometry(displayTask.start_date, displayTask.end_date, timeline);
  const rawLeft = dayjs(displayTask.start_date).diff(timeline.start, 'day') * timeline.dayWidth;
  const rawWidth = taskBarWidth(displayTask, timeline.dayWidth);
  const originalLeft = dayjs(task.start_date).diff(timeline.start, 'day') * timeline.dayWidth;
  const originalWidth = taskBarWidth(task, timeline.dayWidth);
  const computedHandles = ganttEditHandles(originalLeft, originalWidth, task.progress, Math.max(0, viewport.start), Math.min(timeline.width, viewport.end));
  const handles = pointer.current?.handles ?? computedHandles;
  const editable = edit.enabled && task.can_edit && finePointer;
  const sourceSession = edit.draft?.rowId === rowId && edit.phase === 'dragging';
  const enabled = !edit.busy || sourceSession;
  const progressEnabled = editable && canEditProgress(task) && handles.progress && !task.is_milestone;
  const color = task.status === 'completed' ? theme.palette.success.main : task.status === 'in_progress' ? theme.palette.primary.main : theme.palette.grey[task.status === 'cancelled' ? 500 : 600];

  const release = (session: PointerSession) => {
    if (session.element.hasPointerCapture(session.pointer)) session.element.releasePointerCapture(session.pointer);
  };
  const cancelPointer = React.useCallback(() => {
    const session = pointer.current;
    pointer.current = null; keyboard.current = null;
    if (session) { suppressClick.current = performance.now() + 500; release(session); }
    latestEdit.current.cancel();
  }, []);
  React.useEffect(() => {
    const blur = () => { if (pointer.current || keyboard.current) cancelPointer(); };
    const hidden = () => { if (document.hidden) blur(); };
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape' && (pointer.current || keyboard.current)) { event.preventDefault(); blur(); } };
    window.addEventListener('blur', blur); document.addEventListener('visibilitychange', hidden); document.addEventListener('keydown', escape);
    return () => {
      window.removeEventListener('blur', blur); document.removeEventListener('visibilitychange', hidden); document.removeEventListener('keydown', escape);
      const session = pointer.current; pointer.current = null; if (session) release(session);
    };
  }, [cancelPointer]);
  React.useEffect(() => {
    if (edit.phase !== 'dragging') {
      const session = pointer.current; pointer.current = null; keyboard.current = null; if (session) release(session);
    }
  }, [edit.phase]);

  const down = (event: React.PointerEvent<HTMLElement>, operation: GanttEditOperation) => {
    if (event.button !== 0 || !event.isPrimary || event.pointerType === 'touch' || !editable || !enabled) return;
    event.stopPropagation();
    const original = edit.begin(task, rowId, operation, event.currentTarget);
    if (!original) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    pointer.current = { original, latest: original, x: event.clientX, scroll: scrollRef.current?.scrollLeft ?? 0,
      pointer: event.pointerId, moved: false, element: event.currentTarget, handles: computedHandles };
  };
  const move = (event: React.PointerEvent<HTMLElement>) => {
    const session = pointer.current;
    if (!session || session.pointer !== event.pointerId) return;
    const dx = event.clientX - session.x + (scrollRef.current?.scrollLeft ?? 0) - session.scroll;
    if (!session.moved && Math.abs(dx) < DRAG_THRESHOLD) return;
    session.moved = true;
    session.latest = offsetGanttDraft(session.original, dx, timeline.dayWidth);
    edit.preview(session.latest);
  };
  const up = (event: React.PointerEvent<HTMLElement>) => {
    const session = pointer.current;
    if (!session || session.pointer !== event.pointerId) return;
    move(event);
    pointer.current = null;
    if (session.moved || session.original.operation !== 'move') suppressClick.current = performance.now() + 500;
    release(session);
    if (session.moved) edit.finish(session.latest); else edit.cancel();
  };
  const pointerProps = {
    onPointerMove: move, onPointerUp: up,
    onPointerCancel: () => { if (pointer.current) cancelPointer(); },
    onLostPointerCapture: () => { if (pointer.current) cancelPointer(); },
  };
  const open = () => { if (performance.now() >= suppressClick.current && !edit.busy) onOpenTask(task.id); };
  if (!geometry.visible) return null;
  const title = `${task.name}\n${displayTask.start_date} ～ ${displayTask.end_date}\n进度 ${displayTask.progress}%`;
  const bodySx = {
    position: 'absolute', left: geometry.left, top: task.is_milestone ? 13 : 11,
    width: task.is_milestone ? 16 : geometry.width, height: task.is_milestone ? 16 : 22,
    transform: task.is_milestone ? 'rotate(45deg)' : undefined,
    borderRadius: task.is_milestone ? 0.5 : 1, p: 0,
    border: task.is_overdue ? `1px solid ${theme.palette.error.main}` : 0,
    backgroundColor: color, color: 'common.white', overflow: 'hidden', cursor: editable ? 'grab' : 'pointer',
    opacity: task.status === 'cancelled' ? 0.58 : 1,
    outline: active || ownPreview ? `2px solid ${theme.palette.warning.main}` : 'none', outlineOffset: 2,
    '&:focus-visible': { outline: `2px solid ${theme.palette.warning.main}` },
  } as const;
  const progressLeft = progressHandleLeft(rawWidth, displayTask.progress);
  const boundary = rawWidth * displayTask.progress / 100;
  return <>
    <Tooltip title={<Box sx={{ whiteSpace: 'pre-line' }}>{title}</Box>} disableHoverListener={edit.busy}>
      <Box component="button" type="button" aria-label={`打开${task.is_milestone ? '里程碑' : '任务'} ${task.name}`}
        data-gantt-bar={task.id} onClick={open} onPointerDown={(event) => down(event, 'move')} {...pointerProps} sx={bodySx}>
        {!task.is_milestone ? <>
          <Box aria-hidden sx={{ position: 'absolute', top: 0, bottom: 0, left: 0,
            width: Math.max(0, Math.min(geometry.width, rawLeft + boundary - geometry.left)), backgroundColor: 'rgba(0,0,0,0.16)' }} />
          {!progressEnabled && geometry.width >= 34 ? <Typography variant="caption" sx={{ position: 'absolute', right: 5, top: 2, fontSize: '0.72rem', fontWeight: 600 }}>{displayTask.progress}%</Typography> : null}
        </> : null}
      </Box>
    </Tooltip>
    {editable && !task.is_milestone ? <>
      {(['resize-start', 'resize-end'] as const).map((operation) => {
        const start = operation === 'resize-start';
        if (!(start ? handles.start : handles.end)) return null;
        const edge = start ? rawLeft : rawLeft + rawWidth;
        return <React.Fragment key={operation}>
          <Box aria-hidden data-gantt-gap={operation} sx={{ position: 'absolute', top: 9, height: 26, left: start ? edge - DATE_HANDLE_GAP : edge, width: DATE_HANDLE_GAP }} />
          <Tooltip title={start ? '拖动修改开始日期' : '拖动修改截止日期'} disableHoverListener={edit.busy}>
            <Box component="button" type="button" aria-label={`${start ? '修改开始日期' : '修改截止日期'} ${task.name}`} tabIndex={-1}
              data-gantt-handle={operation} disabled={!enabled}
              onPointerDown={(event) => down(event, operation)} {...pointerProps} onClick={(event) => event.stopPropagation()}
              sx={{ position: 'absolute', left: start ? edge - DATE_HANDLE_GAP - DATE_HANDLE_WIDTH : edge + DATE_HANDLE_GAP,
                top: 10, height: 24, width: DATE_HANDLE_WIDTH, border: 1, borderColor: 'divider', p: 0,
                borderRadius: 0.5, backgroundColor: 'background.paper', cursor: 'ew-resize', touchAction: 'none',
                '&:after': { content: '""', display: 'block', width: 2, height: 12, mx: 'auto', backgroundColor: color } }} />
          </Tooltip>
        </React.Fragment>;
      })}
    </> : null}
    {progressEnabled ? <Tooltip title="拖动修改进度；方向键调整，Enter 确认" disableHoverListener={edit.busy}>
      <Box role="slider" tabIndex={enabled ? 0 : -1} aria-label={`修改进度 ${task.name}`} aria-valuemin={0} aria-valuemax={100}
        aria-valuenow={displayTask.progress} aria-valuetext={`${displayTask.progress}%`} aria-disabled={!enabled}
        data-gantt-handle="progress" onPointerDown={(event) => down(event, 'progress')} {...pointerProps}
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && keyboard.current) { event.preventDefault(); const value = keyboard.current; keyboard.current = null; edit.finish(value); return; }
          if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault();
          const value = keyboard.current ?? edit.begin(task, rowId, 'progress', event.currentTarget);
          if (!value) return;
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? 100 : Math.max(0, Math.min(100, value.progress + (['ArrowLeft', 'ArrowDown'].includes(event.key) ? -1 : 1)));
          keyboard.current = { ...value, progress: next }; edit.preview(keyboard.current);
        }}
        onBlur={() => { if (keyboard.current) { const value = keyboard.current; keyboard.current = null; edit.finish(value); } }}
        sx={{ position: 'absolute', left: rawLeft + progressLeft, top: 9, width: PROGRESS_HANDLE_WIDTH, height: 26,
          cursor: 'ew-resize', touchAction: 'none', zIndex: 1, outline: 'none',
          '&:after': { content: '""', position: 'absolute', left: Math.max(0, Math.min(PROGRESS_HANDLE_WIDTH - 6, boundary - progressLeft - 3)),
            top: 0, bottom: 0, width: 6, backgroundColor: 'background.paper', border: `1px solid ${theme.palette.text.primary}`, borderRadius: 0.5 },
          '&:hover:after, &:focus-visible:after': { boxShadow: `0 0 0 2px ${theme.palette.warning.main}` } }} />
    </Tooltip> : null}
    {sourceSession && ownPreview ? <Box sx={{ position: 'absolute', left: geometry.left, top: 31, zIndex: 3,
      px: 0.5, bgcolor: 'background.paper', color: 'text.primary', fontSize: 11, whiteSpace: 'nowrap', pointerEvents: 'none' }}>
      {ownPreview.operation === 'progress' ? `${ownPreview.progress}%` : `${ownPreview.start} ～ ${ownPreview.end}`}
    </Box> : null}
  </>;
}
