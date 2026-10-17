/**
 * 计划视图的筛选栏（个人计划与组内计划共用）。
 *
 * 与 `PlanViewQuery` 的关系（《A06》§1）：
 * - **未知查询参数一律 400**，所以"没选"的项必须**从查询串里消失**（空串、`false` 都不能发）；
 * - `overdue=false` 等价于不筛选、`include_archived` 默认就是 `false`，因此这两个开关**只在打开时**才写进查询；
 * - `priority` 的 `0`（低）是**合法值**，判断"是否选中"必须用 `''` 而不是 falsy；
 * - `due_from > due_to` 服务端会 `400 invalid_date_range`，这里**先在本地拦住**并禁用查询按钮。
 *
 * "只看我…"用一个下拉映射三个互斥参数（`task_owner_id` / `task_member_id` / `project_owner_id`）：
 * 选人需要用户清单，而 `POST /users/query` 是系统管理员专用（《A02》§端点 5），
 * 所以这里只提供"我"这一个维度（F04 D29）。
 */

import * as React from 'react';
import Box from '@mui/material/Box';
import Stack from '@mui/material/Stack';
import Button from '@mui/material/Button';
import MenuItem from '@mui/material/MenuItem';
import TextField from '@mui/material/TextField';
import FormControlLabel from '@mui/material/FormControlLabel';
import Switch from '@mui/material/Switch';
import SearchIcon from '@mui/icons-material/Search';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import { DatePicker } from '@mui/x-date-pickers/DatePicker';
import DateLocalizationProvider from '@/components/common/DateLocalizationProvider';
import { TASK_PRIORITY_OPTIONS, TASK_STATUS_OPTIONS } from '@/components/task/taskMeta';
import { PROJECT_STATUS_OPTIONS } from '@/components/project/projectMeta';
import type { PlanProject, PlanViewQuery } from '@/types/plan';
import type { ProjectStatus } from '@/types/project';
import type { TaskPriority, TaskStatus } from '@/types/task';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import { dayjsToNaturalDate, naturalDateToDayjs } from '@/utils/datetime';

/** "只看"的四种口径；后三种各自映射到一个查询参数 */
export type PlanScope = 'all' | 'task_owner' | 'task_member' | 'project_owner';

/** "只看"下拉的选项：菜单与 `renderValue` 共用一份文案，避免两处写得不一致 */
export const PLAN_SCOPE_OPTIONS: { value: PlanScope; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'task_owner', label: '我负责的任务' },
  { value: 'task_member', label: '我参与的任务' },
  { value: 'project_owner', label: '我持有的项目' },
];

export interface PlanFilterForm {
  project_id: string;
  scope: PlanScope;
  project_status: ProjectStatus | '';
  task_status: TaskStatus | '';
  /** `''` 表示不限；`0` 是合法的"低"优先级 */
  priority: TaskPriority | '';
  due_from: string;
  due_to: string;
  overdue: boolean;
  include_archived: boolean;
}

export const DEFAULT_PLAN_FILTERS: PlanFilterForm = {
  project_id: '',
  scope: 'all',
  project_status: '',
  task_status: '',
  priority: '',
  due_from: '',
  due_to: '',
  overdue: false,
  include_archived: false,
};

/** 表单 → 查询参数：空值一律省略（未知/空参数会被服务端拒绝） */
export function buildPlanQuery(form: PlanFilterForm, currentUserId: string | null): PlanViewQuery {
  const query: PlanViewQuery = {};
  if (form.project_id) {
    query.project_id = form.project_id;
  }
  if (form.scope !== 'all' && currentUserId) {
    if (form.scope === 'task_owner') {
      query.task_owner_id = currentUserId;
    } else if (form.scope === 'task_member') {
      query.task_member_id = currentUserId;
    } else {
      query.project_owner_id = currentUserId;
    }
  }
  if (form.project_status) {
    query.project_status = form.project_status;
  }
  if (form.task_status) {
    query.task_status = form.task_status;
  }
  if (form.priority !== '') {
    query.priority = form.priority;
  }
  if (form.due_from) {
    query.due_from = form.due_from;
  }
  if (form.due_to) {
    query.due_to = form.due_to;
  }
  if (form.overdue) {
    query.overdue = true;
  }
  if (form.include_archived) {
    query.include_archived = true;
  }
  return query;
}

/** 本地校验：日期区间倒置（服务端会 400，这里先拦） */
export function planFilterDateError(form: PlanFilterForm): string | null {
  if (form.due_from && form.due_to && form.due_from > form.due_to) {
    return '截止区间倒置：开始不晚于结束';
  }
  return null;
}

/** 把一组查询参数还原成表单（"重置"与回填都用它） */
export function planFiltersFromQuery(query: PlanViewQuery, currentUserId: string | null): PlanFilterForm {
  const scope: PlanScope = !currentUserId
    ? 'all'
    : query.task_owner_id
      ? 'task_owner'
      : query.task_member_id
        ? 'task_member'
        : query.project_owner_id
          ? 'project_owner'
          : 'all';
  return {
    project_id: query.project_id ?? '',
    scope,
    project_status: query.project_status ?? '',
    task_status: query.task_status ?? '',
    priority: query.priority ?? '',
    due_from: query.due_from ?? '',
    due_to: query.due_to ?? '',
    overdue: query.overdue === true,
    include_archived: query.include_archived === true,
  };
}

export interface PlanFiltersProps {
  value: PlanFilterForm;
  onChange: (next: PlanFilterForm) => void;
  onApply: () => void;
  onReset: () => void;
  /** 项目下拉的选项来源：**未筛选那次**响应的 `projects`（见 usePlanView） */
  projectOptions: PlanProject[];
  loading: boolean;
  /** 组内计划传入工作组选择器；个人计划不传 */
  groupSlot?: React.ReactNode;
}

export default function PlanFilters({
  value,
  onChange,
  onApply,
  onReset,
  projectOptions,
  loading,
  groupSlot,
}: PlanFiltersProps) {
  const dateError = planFilterDateError(value);
  const patch = (part: Partial<PlanFilterForm>) => onChange({ ...value, ...part });

  return (
    // 日期组件需要的 provider 跟着"用它的组件"走（F01 §6 第 23 条）
    <DateLocalizationProvider>
      {/*
        筛选区：一条底部分割线就够（不加背景色），内部固定三层——
        ① 全部选择项；② 截止区间；③ 两个开关 + 查询/重置。
        后两层内容通常占不满一行，靠右排，避免右侧留一大片空白。
      */}
      <Box sx={{ pb: 2, borderBottom: 1, borderColor: 'divider' }}>
        <Stack spacing={1.5}>
          {/* ① 选择项：随宽度折行，左对齐 */}
          <Box sx={filterRowSx(false)}>
              {/* 工作组选择器由组内计划页传入；宽度规则统一在这里给，避免两处各写一套 */}
              {groupSlot ? <Box sx={filterItemSx(130)}>{groupSlot}</Box> : null}

            <TextField
              select
              size="small"
              label="项目"
              value={value.project_id}
              onChange={(event) => patch({ project_id: event.target.value })}
              sx={filterItemSx(130)}
              // 空值也显示占位文案 + 标签常驻凹槽：否则同一行里"有值的下拉"标签会浮在框外，
              // 看起来不像一套控件。注意这两个 prop 属于 Select，要经 slotProps.select 传
              // （直接写在 TextField 上 TS 会报"不存在该属性"）
              slotProps={{
                inputLabel: { shrink: true },
                select: {
                  displayEmpty: true,
                  renderValue: (selected: unknown) => {
                    const id = String(selected ?? '');
                    if (id === '') {
                      return '全部项目';
                    }
                    return projectOptions.find((project) => project.id === id)?.name ?? '全部项目';
                  },
                },
              }}
            >
              <MenuItem value="">全部项目</MenuItem>
              {projectOptions.map((project) => (
                <MenuItem key={project.id} value={project.id}>
                  {project.name}
                </MenuItem>
              ))}
            </TextField>

            <TextField
              select
              size="small"
              label="只看"
              value={value.scope}
              onChange={(event) => patch({ scope: event.target.value as PlanScope })}
              sx={filterItemSx(140)}
              slotProps={{
                inputLabel: { shrink: true },
                select: {
                  renderValue: (selected: unknown) =>
                    PLAN_SCOPE_OPTIONS.find((option) => option.value === selected)?.label ?? '全部',
                },
              }}
            >
              {PLAN_SCOPE_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>

            <TextField
              select
              size="small"
              label="项目状态"
              value={value.project_status}
              onChange={(event) => patch({ project_status: event.target.value as ProjectStatus | '' })}
              sx={filterItemSx(130)}
              slotProps={{
                inputLabel: { shrink: true },
                select: {
                  displayEmpty: true,
                  renderValue: (selected: unknown) =>
                    PROJECT_STATUS_OPTIONS.find((option) => option.value === selected)?.label ??
                    '不限',
                },
              }}
            >
              <MenuItem value="">不限</MenuItem>
              {PROJECT_STATUS_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>

            <TextField
              select
              size="small"
              label="任务状态"
              value={value.task_status}
              onChange={(event) => patch({ task_status: event.target.value as TaskStatus | '' })}
              sx={filterItemSx(130)}
              slotProps={{
                inputLabel: { shrink: true },
                select: {
                  displayEmpty: true,
                  renderValue: (selected: unknown) =>
                    TASK_STATUS_OPTIONS.find((option) => option.value === selected)?.label ?? '不限',
                },
              }}
            >
              <MenuItem value="">不限</MenuItem>
              {TASK_STATUS_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>

            <TextField
              select
              size="small"
              label="优先级"
              value={value.priority === '' ? '' : String(value.priority)}
              onChange={(event) =>
                patch({
                  priority: event.target.value === '' ? '' : (Number(event.target.value) as TaskPriority),
                })
              }
              sx={filterItemSx(120)}
              slotProps={{
                inputLabel: { shrink: true },
                select: {
                  displayEmpty: true,
                  renderValue: (selected: unknown) =>
                    TASK_PRIORITY_OPTIONS.find(
                      (option) => String(option.value) === String(selected ?? ''),
                    )?.label ?? '不限',
                },
              }}
            >
              <MenuItem value="">不限</MenuItem>
              {TASK_PRIORITY_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={String(option.value)}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>

          </Box>

          {/* ② 截止区间：靠右；出现错误说明时用顶部对齐，避免两个输入框错位 */}
          <Box sx={filterRowSx(true, true)}>
              <DatePicker
                label="截止（起）"
              value={naturalDateToDayjs(value.due_from)}
              onChange={(next) => patch({ due_from: dayjsToNaturalDate(next) ?? '' })}
              format="YYYY-MM-DD"
              slotProps={{
                textField: {
                  size: 'small',
                  sx: { width: 160 },
                  error: Boolean(dateError),
                },
              }}
            />
            <DatePicker
              label="截止（止）"
              value={naturalDateToDayjs(value.due_to)}
              onChange={(next) => patch({ due_to: dayjsToNaturalDate(next) ?? '' })}
              format="YYYY-MM-DD"
              slotProps={{
                textField: {
                  size: 'small',
                  sx: { width: 160 },
                  error: Boolean(dateError),
                  helperText: dateError ?? undefined,
                },
              }}
            />

          </Box>

          {/* ③ 开关 + 操作：靠右。FormControlLabel 默认带外边距，归零后这一行的间距
              （16px）才与上面两层完全一致 */}
          <Box sx={filterRowSx(true)}>
            <FormControlLabel
              control={
                <Switch
                  checked={value.overdue}
                  onChange={(event) => patch({ overdue: event.target.checked })}
                />
              }
              label="只看逾期"
              sx={{ m: 0 }}
            />
            <FormControlLabel
              control={
                <Switch
                  checked={value.include_archived}
                  onChange={(event) => patch({ include_archived: event.target.checked })}
                />
              }
              label="含归档项目"
              sx={{ m: 0 }}
            />

            <Button
              variant="contained"
              startIcon={<SearchIcon />}
              onClick={onApply}
              disabled={loading || Boolean(dateError)}
            >
              查询
            </Button>
              <Button variant="outlined" startIcon={<RestartAltIcon />} onClick={onReset}>
                重置
              </Button>
          </Box>
        </Stack>
      </Box>
    </DateLocalizationProvider>
  );
}