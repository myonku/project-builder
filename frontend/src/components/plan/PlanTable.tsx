/**
 * 计划视图的表格：**项目行 + 缩进的任务行**合成一张表（行模型见 `planRows.ts`）。
 *
 * 与列表页（F01 §5.9）的差异，都是《A06》决定的：
 * - **无分页**：三个计划视图端点都不分页，`projects` / `tasks` 是完整数组；
 * - **不排序**：服务端已按 `end_date` / `start_date` 固定顺序，客户端重排会打乱"项目 → 任务"的分组；
 * - **不筛选**：筛选走查询参数（服务端做），所以关掉列头菜单；
 * - 任务行点击打开 `TaskDetailDrawer`（`?taskId=`），项目行点击进项目详情。
 *
 * 两个单元格相关的坑在这里都踩不到，因为用的是共享组件：
 * 进度用 `ProgressCell`（块级容器自己撑满单元格，F01 §6 第 28 条），
 * 纯文本用 `CellText`（行内元素，第 22 条）。
 */

import * as React from 'react';
import Box from '@mui/material/Box';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import SubdirectoryArrowRightIcon from '@mui/icons-material/SubdirectoryArrowRight';
import FlagOutlinedIcon from '@mui/icons-material/FlagOutlined';
import GroupOutlinedIcon from '@mui/icons-material/GroupOutlined';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams, GridRowClassNameParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import CellText from '@/components/common/CellText';
import ProgressCell from '@/components/common/ProgressCell';
import StatusTag from '@/components/common/StatusTag';
import { truncatedText } from '@/components/common/TruncatedText';
import { isProjectRow, planRowEnd, planRowStart, type PlanRow } from '@/components/plan/planRows';
import { projectStatusMeta } from '@/components/project/projectMeta';
import { taskPriorityMeta, taskStatusMeta } from '@/components/task/taskMeta';
import { formatForDisplay, formatNaturalDate } from '@/utils/datetime';
import type { PlanRole } from '@/types/plan';

export interface PlanTableProps {
  rows: PlanRow[];
  loading: boolean;
  /** 任务行点击（打开抽屉） */
  onOpenTask: (taskId: string) => void;
  /** 项目行点击（去项目详情） */
  onOpenProject: (projectId: string) => void;
  /** 当前打开的抽屉任务（高亮所在行） */
  activeTaskId?: string | null;
  /** 用户时区（`updated_at` 的展示口径与详情页一致） */
  timeZone?: string;
  /** 空数据时的提示语 */
  emptyHint?: string;
}

/** "我的角色"：个人视图里恒有值；组内视图里 `null` 表示"不是项目成员的组内访客" */
function myRoleTag(role: PlanRole) {
  if (role === 'owner') {
    return <StatusTag label="持有者" tone="primary" />;
  }
  if (role === 'member') {
    return <StatusTag label="成员" tone="default" variant="outlined" />;
  }
  return <StatusTag label="组内访客" tone="info" variant="outlined" />;
}

export default function PlanTable({
  rows,
  loading,
  onOpenTask,
  onOpenProject,
  activeTaskId = null,
  timeZone,
  emptyHint = '当前筛选下没有项目或任务',
}: PlanTableProps) {
  const columns = React.useMemo<GridColDef<PlanRow>[]>(
    () => [
      {
        field: 'name',
        headerName: '项目 / 任务',
        flex: 1,
        // 1440 视口下常用列不横向滚动：下面的固定宽度合计约 1060
        minWidth: 240,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          if (isProjectRow(row)) {
            return (
              <Stack
                direction="row"
                spacing={1}
                sx={{ alignItems: 'center', height: '100%', minWidth: 0 }}
              >
                <Box
                  sx={{
                    width: 10,
                    height: 10,
                    borderRadius: '50%',
                    flexShrink: 0,
                    backgroundColor: row.project.color ?? 'primary.main',
                  }}
                />
                <Typography variant="body2" sx={{ fontWeight: 600 }} noWrap>
                  {row.project.name}
                </Typography>
                {row.project.is_archived ? (
                  <StatusTag label="已归档" tone="default" variant="outlined" />
                ) : null}
              </Stack>
            );
          }
          return (
            <Stack
              direction="row"
              spacing={0.75}
              sx={{ alignItems: 'center', height: '100%', minWidth: 0, pl: (row.depth - 1) * 2.5 }}
            >
              {row.depth > 1 ? (
                <SubdirectoryArrowRightIcon fontSize="inherit" sx={{ color: 'text.disabled' }} />
              ) : null}
              <Typography variant="body2" noWrap title={row.task.name}>
                {row.task.name}
              </Typography>
              {row.task.is_milestone ? (
                <Tooltip title="里程碑">
                  <FlagOutlinedIcon fontSize="inherit" sx={{ color: 'warning.main' }} />
                </Tooltip>
              ) : null}
              {row.task.is_shared ? (
                <Tooltip title="已共享给工作组（组内计划里能看到它）">
                  <GroupOutlinedIcon fontSize="inherit" sx={{ color: 'text.disabled' }} />
                </Tooltip>
              ) : null}
            </Stack>
          );
        },
      },
      {
        field: 'status',
        headerName: '状态',
        width: 100,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          const meta = isProjectRow(row)
            ? projectStatusMeta(row.project.status)
            : taskStatusMeta(row.task.status);
          return <StatusTag label={meta.label} tone={meta.tone} />;
        },
      },
      {
        field: 'priority',
        headerName: '优先级',
        width: 90,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          if (isProjectRow(row)) {
            return <CellText muted>—</CellText>;
          }
          const meta = taskPriorityMeta(row.task.priority);
          return <StatusTag label={meta.label} tone={meta.tone} variant="outlined" />;
        },
      },
      {
        field: 'progress',
        headerName: '进度',
        width: 130,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => (
          <ProgressCell
            value={isProjectRow(params.row) ? params.row.project.progress : params.row.task.progress}
          />
        ),
      },
      {
        field: 'start_date',
        headerName: '开始',
        width: 105,
        sortable: false,
        valueGetter: (_value, row) => planRowStart(row),
        valueFormatter: (value) => formatNaturalDate(value as string | null),
      },
      {
        field: 'end_date',
        headerName: '结束',
        width: 105,
        sortable: false,
        valueGetter: (_value, row) => planRowEnd(row),
        valueFormatter: (value) => formatNaturalDate(value as string | null),
      },
      {
        field: 'overdue',
        headerName: '逾期',
        width: 90,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          if (isProjectRow(row)) {
            return row.project.overdue_task_count > 0 ? (
              <StatusTag label={`${row.project.overdue_task_count} 个`} tone="error" />
            ) : (
              <CellText muted>—</CellText>
            );
          }
          return row.task.is_overdue ? (
            <StatusTag label="已逾期" tone="error" />
          ) : (
            <CellText muted>—</CellText>
          );
        },
      },
      {
        field: 'owner',
        headerName: '负责人 / 我的角色',
        width: 130,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          if (isProjectRow(row)) {
            return myRoleTag(row.project.my_role);
          }
          const owner = row.task.owner;
          if (!owner) {
            return <CellText muted>未指定</CellText>;
          }
          return (
            <Tooltip title={`${owner.display_name}（${owner.username}）`}>
              <span>{truncatedText(owner.display_name, { typographyProps: { variant: 'body2' } })}</span>
            </Tooltip>
          );
        },
      },
      {
        field: 'members',
        headerName: '成员',
        width: 200,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          if (isProjectRow(row)) {
            return <CellText muted>—</CellText>;
          }
          if (row.task.members.length === 0) {
            return <CellText muted>无</CellText>;
          }
          const names = row.task.members.map((member) => member.display_name).join('、');
          return (
            <Tooltip title={names}>
              <span>{truncatedText(names, { typographyProps: { variant: 'body2' } })}</span>
            </Tooltip>
          );
        },
      },
      {
        field: 'updated_at',
        headerName: '最近更新',
        width: 160,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => (
          <CellText muted>
            {formatForDisplay(
              isProjectRow(params.row) ? params.row.project.updated_at : params.row.task.updated_at,
              timeZone,
            )}
          </CellText>
        ),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 70,
        sortable: false,
        renderCell: (params: GridRenderCellParams<PlanRow>) => {
          const row = params.row;
          const isProject = isProjectRow(row);
          const label = isProject ? row.project.name : row.task.name;
          const targetId = isProject ? row.project.id : row.task.id;
          return (
            <Tooltip title={isProject ? '打开项目详情' : '打开任务详情'}>
              <IconButton
                size="small"
                aria-label={`打开${isProject ? '项目' : '任务'} ${label}`}
                onClick={(event) => {
                  event.stopPropagation();
                  if (isProject) {
                    onOpenProject(targetId);
                  } else {
                    onOpenTask(targetId);
                  }
                }}
              >
                <OpenInNewIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          );
        },
      },
    ],
    [onOpenProject, onOpenTask, timeZone],
  );

  return (
    <DataGrid<PlanRow>
      rows={rows}
      columns={columns}
      getRowId={(row) => row.id}
      loading={loading}
      disableColumnFilter
      disableColumnMenu
      disableRowSelectionOnClick
      hideFooter
      getRowClassName={(params: GridRowClassNameParams<PlanRow>) =>
        params.row.kind === 'project'
          ? 'plan-row-project'
          : params.row.id === activeTaskId
            ? 'plan-row-active'
            : ''
      }
      onRowClick={(params) => {
        if (isProjectRow(params.row)) {
          onOpenProject(params.row.project.id);
        } else {
          onOpenTask(params.row.task.id);
        }
      }}
      slots={{
        noRowsOverlay: () => (
          <Stack sx={{ alignItems: 'center', justifyContent: 'center', height: '100%' }} spacing={0.5}>
            <Typography variant="body2" color="text.secondary">
              {emptyHint}
            </Typography>
          </Stack>
        ),
      }}
      sx={{
        '& .plan-row-project': {
          backgroundColor: 'action.hover',
        },
        '& .plan-row-project .MuiDataGrid-cell': {
          fontWeight: 600,
        },
        '& .plan-row-active': {
          backgroundColor: 'action.selected',
        },
        border: 0,
      }}
      localeText={zhCN.components.MuiDataGrid.defaultProps.localeText}
      initialState={{
        columns: {
          // 成员与最近更新默认收起：常用列在 1440 视口内不横向滚动（列菜单可打开）
          columnVisibilityModel: { members: false, updated_at: false },
        },
      }}
    />
  );
}
