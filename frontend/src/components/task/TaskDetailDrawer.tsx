/**
 * 任务详情抽屉（`?taskId=` 承载）——F05 §7.2 第 2/3 步。
 *
 * 为什么用 Drawer 而不是路由页：任务单端点挂在 `/api/v1/tasks/{task_id}`（与项目无嵌套），
 * 且几乎总是从项目详情或计划视图点开——Drawer 关掉就回到原列表，筛选与滚动位置天然保留；
 * 项目详情、个人计划、组内计划**三处复用同一个组件**（《F01》§5.7）。
 *
 * 本组件负责：
 * 1. 用 `useVersionedForm` 加载任务（同时拿到 `updated_at` 供 PATCH 使用）；
 * 2. **编辑任务**：`TaskFormDialog` + `buildTaskPatch`（只提交改动字段，含 `parent_id` 三态），
 *    `409 version_conflict` 交给 Hook 弹窗二选一；
 * 3. **任务成员管理**：`TaskMembersPanel`（候选池是项目成员，《A05》§端点 9）。
 *
 * `canEdit` 优先使用调用方已有的服务端投影；未提供时按当前任务读取权限；归档只读，
 * 列表未包含当前任务时，使用既有项目甘特图投影读取 can_edit，而不沿用子任务权限（F01 §6 第 7、8、10 条）。
 *
 * 刷新语义：`taskId` 变化时由调用方用 `key={taskId}` 重新挂载本组件，
 * 因此 `autoLoad` 只跑一次就够——**不在这里用 effect 手动重载**，那样会与 Hook 自己的加载 effect 打架。
 */

import * as React from 'react';
import { useSearchParams } from 'react-router';
import Link from '@mui/material/Link';
import { getProjectGantt } from '@/api/planViews';
import { truncatedText } from '@/components/common/TruncatedText';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Divider from '@mui/material/Divider';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import EditIcon from '@mui/icons-material/Edit';
import { queryProjectMembers } from '@/api/projects';
import { getTask, updateTask } from '@/api/tasks';
import { toApiError, type ApiError } from '@/api/client';
import AttachmentPanel from '@/components/attachment/AttachmentPanel';
import FullPageLoading from '@/components/common/FullPageLoading';
import StatusTag from '@/components/common/StatusTag';
import TaskFormDialog, {
  buildTaskPatch,
  type TaskFormValues,
} from '@/components/task/TaskFormDialog';
import TaskMembersPanel from '@/components/task/TaskMembersPanel';
import { taskPriorityMeta, taskStatusMeta } from '@/components/task/taskMeta';
import { describeApiError } from '@/hooks/useApiError';
import { useNotifications } from '@/hooks/useNotifications';
import { useVersionedForm } from '@/hooks/useVersionedForm';
import { useAuthStore } from '@/stores/auth';
import type { ProjectMember } from '@/types/project';
import type { Task, UpdateTaskBody } from '@/types/task';
import { formatForDisplay, formatNaturalDate } from '@/utils/datetime';

export interface TaskDetailDrawerProps {
  /** 打开的任务 id；`null` 表示关闭 */
  taskId: string | null;
  /** 已有服务端 can_edit 投影可直接传入；省略时从当前任务的项目甘特图投影读取。 */
  canEdit?: boolean;
  /** 任务数据变化后通知调用方（刷新任务列表） */
  onChanged?: () => void;
  onClose: () => void;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack
      direction="row"
      spacing={2}
      sx={{ justifyContent: 'space-between', alignItems: 'center' }}
    >
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Box sx={{ textAlign: 'right', minWidth: 0 }}>{children}</Box>
    </Stack>
  );
}

export default function TaskDetailDrawer({
  taskId,
  canEdit: knownCanEdit,
  onChanged,
  onClose,
}: TaskDetailDrawerProps) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [resolvedCanEdit, setResolvedCanEdit] = React.useState(false);
  const canEdit = knownCanEdit ?? resolvedCanEdit;
  const [parent, setParent] = React.useState<{ id: string; name: string } | null>(null);
  const [parentLoading, setParentLoading] = React.useState(false);
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const { show } = useNotifications();

  const [editOpen, setEditOpen] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState<ApiError | null>(null);
  const [projectMembers, setProjectMembers] = React.useState<ProjectMember[]>([]);

  const {
    entity: task,
    loading,
    error,
    save: saveTask,
  } = useVersionedForm<Task, UpdateTaskBody>({
    load: (signal) => getTask(taskId as string, signal),
    save: (body) => updateTask(taskId as string, body),
    autoLoad: taskId !== null,
  });

  // Parent tasks may be outside the host page's active filters. Resolve their names independently.
  React.useEffect(() => {
    setParent(null);
    if (!task?.parent_id) { setParentLoading(false); return; }
    const controller = new AbortController();
    let cancelled = false;
    setParentLoading(true);
    void getTask(task.parent_id, controller.signal).then(value => {
      if (!cancelled) setParent({ id: value.id, name: value.name });
    }).catch(() => { /* Keep inaccessible parents unnamed and unclickable. */ })
      .finally(() => { if (!cancelled) setParentLoading(false); });
    return () => { cancelled = true; controller.abort(); };
  }, [task?.parent_id, task?.updated_at]);

  // When a parent was filtered out of the host payload, use the existing authoritative projection.
  React.useEffect(() => {
    setResolvedCanEdit(false);
    if (knownCanEdit !== undefined || !task) return;
    const controller = new AbortController();
    let cancelled = false;
    void getProjectGantt(task.project_id, controller.signal).then(value => {
      if (!cancelled) setResolvedCanEdit(value.tasks.find(item => item.id === task.id)?.can_edit ?? false);
    }).catch(() => { /* Default to read-only if permission context cannot be loaded. */ });
    return () => { cancelled = true; controller.abort(); };
  }, [knownCanEdit, task?.id, task?.project_id, task?.updated_at]);

  // 任务成员的候选池是"项目成员"：拿到任务后按 `project_id` 拉一次
  const projectId = task?.project_id;
  React.useEffect(() => {
    if (!projectId || !canEdit) {
      setProjectMembers([]);
      return undefined;
    }
    const controller = new AbortController();
    let cancelled = false;
    void (async () => {
      try {
        const page = await queryProjectMembers(
          projectId,
          { page: 1, page_size: 100, sort: 'display_name' },
          controller.signal,
        );
        if (!cancelled) {
          setProjectMembers(page.data ?? []);
        }
      } catch (caught) {
        // 候选池拿不到只影响"加入成员"，不影响详情展示
        if (!cancelled && toApiError(caught).code !== 'aborted') {
          setProjectMembers([]);
        }
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [canEdit, projectId]);

  const handleSave = React.useCallback(
    async (values: TaskFormValues) => {
      if (!task) {
        return;
      }
      const patch = buildTaskPatch(values, task);
      if (Object.keys(patch).length === 0) {
        show('没有需要保存的修改。', { severity: 'info' });
        setEditOpen(false);
        return;
      }
      setSaving(true);
      setSaveError(null);
      const outcome = await saveTask(patch);
      setSaving(false);
      if (outcome.ok) {
        setEditOpen(false);
        onChanged?.();
        show('任务已保存。', { severity: 'success' });
        return;
      }
      if (outcome.resolution === 'reloaded') {
        setEditOpen(false);
        onChanged?.();
        show('已加载最新信息。', { severity: 'info' });
        return;
      }
      setSaveError(outcome.error);
      if (outcome.resolution === 'kept') {
        show('已保留你的修改，请检查后再次保存。', { severity: 'warning' });
      }
    },
    [onChanged, saveTask, show, task],
  );

  const statusMeta = taskStatusMeta(task?.status);
  const priorityMeta = taskPriorityMeta(task?.priority);

  return (
    <Drawer
      anchor="right"
      open={taskId !== null}
      onClose={onClose}
      slotProps={{
        paper: {
          sx: {
            width: { xs: '100%', sm: 640, md: 720 },
            maxWidth: '100vw',
            // 与审计详情抽屉一致：绝对定位的 AppBar 会盖住从视口顶部开始的抽屉
            top: { xs: 56, sm: 64 },
            height: { xs: 'calc(100% - 56px)', sm: 'calc(100% - 64px)' },
          },
        },
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <Stack
          direction="row"
          sx={{ alignItems: 'center', justifyContent: 'space-between', px: 2, py: 1.5 }}
        >
          <Typography variant="h6" component="h2">
            任务详情
          </Typography>
          <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
            <Tooltip title={canEdit ? '' : '归档项目或只读共享下不能修改任务'}>
              <span>
                <Button
                  variant="outlined"
                  startIcon={<EditIcon />}
                  disabled={!canEdit || !task}
                  onClick={() => {
                    setSaveError(null);
                    setEditOpen(true);
                  }}
                >
                  编辑
                </Button>
              </span>
            </Tooltip>
            <Tooltip title="关闭">
              <IconButton onClick={onClose} aria-label="关闭任务详情">
                <CloseIcon />
              </IconButton>
            </Tooltip>
          </Stack>
        </Stack>
        <Divider />

        <Box sx={{ flex: 1, overflow: 'auto', px: 2, py: 2 }}>
          {loading && !task ? <FullPageLoading message="正在加载任务…" /> : null}

          {error ? (
            <Alert severity={error.isNotFound ? 'info' : 'error'}>{describeApiError(error)}</Alert>
          ) : null}

          {task ? (
            <Stack spacing={2}>
              <Stack spacing={1}>
                <Typography variant="h6" component="h3">
                  {task.name}
                </Typography>
                <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: 1 }}>
                  <StatusTag label={statusMeta.label} tone={statusMeta.tone} />
                  <StatusTag
                    label={`优先级 ${priorityMeta.label}`}
                    tone={priorityMeta.tone}
                    variant="outlined"
                  />
                  {task.is_milestone ? (
                    <StatusTag label="里程碑" tone="info" variant="outlined" />
                  ) : null}
                  {task.is_shared ? (
                    <StatusTag label="已共享给组" tone="primary" variant="outlined" />
                  ) : null}
                  {task.is_overdue ? <StatusTag label="已逾期" tone="error" /> : null}
                  {task.deleted_at ? (
                    <StatusTag label="已删除" tone="default" variant="outlined" />
                  ) : null}
                </Stack>
              </Stack>

              {task.deleted_at ? (
                <Alert severity="warning">
                  该任务已删除（{formatForDisplay(task.deleted_at, userTimeZone)}），
                  默认列表中不再显示。
                </Alert>
              ) : null}

              {!canEdit ? (
                <Alert severity="info" variant="outlined">
                  当前状态下任务只读（项目已归档，或你在项目上只有读权限）。
                </Alert>
              ) : null}

              <Divider />
              <Field label="进度">
                <Typography variant="body2">{task.progress}%</Typography>
                {task.has_children ? <Typography variant="caption" color="text.secondary">由直接下级任务等权汇总</Typography> : null}
              </Field>
              <Field label="开始日期">
                <Typography variant="body2">{formatNaturalDate(task.start_date)}</Typography>
              </Field>
              <Field label="结束日期">
                <Typography variant="body2">{formatNaturalDate(task.end_date)}</Typography>
              </Field>
              <Field label="我的角色">
                {task.membership_role ? (
                  <StatusTag
                    label={task.membership_role === 'owner' ? '持有者' : '成员'}
                    tone={task.membership_role === 'owner' ? 'primary' : 'default'}
                    variant="outlined"
                  />
                ) : (
                  <Typography variant="body2" color="text.secondary">
                    非任务成员
                  </Typography>
                )}
              </Field>
              <Field label="上级任务">
                {parent ? <Link component="button" type="button" aria-label={`查看上级任务：${parent.name}`}
                  onClick={() => {
                    const next = new URLSearchParams(searchParams);
                    next.set('taskId', parent.id);
                    setSearchParams(next);
                  }} sx={{ maxWidth: '100%', textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {truncatedText(parent.name, { threshold: 28, keep: 24, ellipsis: '…' })}
                </Link> : <Typography variant="body2" color="text.secondary">
                  {!task.parent_id ? '—' : parentLoading ? '正在读取…' : '上级任务不可用'}
                </Typography>}
              </Field>
              <Field label="最近更新">
                <Typography variant="body2">
                  {formatForDisplay(task.updated_at, userTimeZone)}
                </Typography>
              </Field>

              {task.description ? (
                <>
                  <Divider />
                  <Stack spacing={0.5}>
                    <Typography variant="subtitle2" color="text.secondary">
                      描述
                    </Typography>
                    <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
                      {task.description}
                    </Typography>
                  </Stack>
                </>
              ) : null}

              <Divider />
              <TaskMembersPanel
                key={task.id}
                taskId={task.id}
                canManage={canEdit}
                projectMembers={projectMembers}
                onChanged={onChanged}
              />

              <Divider />
              <AttachmentPanel
                key={`attachments-${task.id}`}
                target={{ kind: 'task', id: task.id }}
                canWrite={canEdit}
                canPurgeAll={canEdit && task.membership_role === 'owner'}
                dense
              />
            </Stack>
          ) : null}
        </Box>
      </Box>

      <TaskFormDialog
        open={editOpen}
        mode="edit"
        projectId={task?.project_id ?? ''}
        task={task}
        submitting={saving}
        serverError={saveError}
        onClose={() => setEditOpen(false)}
        onSubmit={(values) => void handleSave(values)}
      />
    </Drawer>
  );
}
