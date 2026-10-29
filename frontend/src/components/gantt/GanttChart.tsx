/**
 * 甘特图时间轴与编辑入口（P03）。
 *
 * 为什么没有直接用 `frappe-gantt`：本轮实际装了 1.2.2 做可行性验证，但它
 * 没有 TypeScript 声明、没有"成员泳道/层级行"抽象；要把两类视图塞进同一个
 * 共享时间轴，需要在外层自己拼多张图并同步滚动。相比之下，这里的只读需求
 * 只需要"时间轴 + 任务条 + 左侧行标签"，自研简化版更小、更可控（F04 D3）。
 *
 * 组件只负责画：数据行由 `ganttModel.ts` 生成，页面负责取数与筛选。
 * 编辑会话由页面控制器统一管理；组件只上报草稿，不直接写 API。
 */

import * as React from 'react';
import { useTheme } from '@mui/material/styles';
import Box from '@mui/material/Box';
import IconButton from '@mui/material/IconButton';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import FlagOutlinedIcon from '@mui/icons-material/FlagOutlined';
import SubdirectoryArrowRightIcon from '@mui/icons-material/SubdirectoryArrowRight';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import {
  barGeometry,
  buildGanttWindow,
  isMemberGanttRow,
  isProjectGanttRow,
  isTaskGanttRow,
  rowDates,
  type GanttRow,
  type GanttScale,
  type GanttTaskRow,
} from './ganttModel';
import { formatNaturalDate } from '@/utils/datetime';
import UserAvatar from '@/components/common/UserAvatar';
import useMediaQuery from '@mui/material/useMediaQuery';
import EditCalendarOutlinedIcon from '@mui/icons-material/EditCalendarOutlined';
import type { GanttEditController } from '@/hooks/useGanttEdit';
import GanttTaskBar from './GanttTaskBar';
import { canEditProgress } from './ganttEditModel';

export interface GanttChartProps {
  rows: GanttRow[];
  edit: GanttEditController;
  scale: GanttScale;
  /** 左侧第一列表头：项目视图为"项目 / 任务"，成员视图为"成员 / 任务" */
  labelHeader: string;
  today: string;
  collapsedIds: ReadonlySet<string>;
  onToggleRow: (rowId: string) => void;
  onOpenTask: (taskId: string) => void;
  onOpenProject?: (projectId: string) => void;
  activeTaskId?: string | null;
  emptyHint?: string;
}

const LABEL_WIDTH = 300;
const ROW_HEIGHT = 42;

interface TaskRowLabelProps {
  edit: GanttEditController;
  row: GanttTaskRow;
  collapsed: boolean;
  onToggle: () => void;
  onOpenTask: (taskId: string) => void;
}

function TaskRowLabel({ row, collapsed, onToggle, onOpenTask, edit }: TaskRowLabelProps) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', minWidth: 0, width: '100%' }}>
      <Box sx={{ width: 24, flexShrink: 0, display: 'flex', justifyContent: 'center' }}>
        {row.hasChildren ? (
          <IconButton
            size="small"
            aria-label={collapsed ? '展开子任务' : '收起子任务'}
            disabled={edit.busy}
            onClick={onToggle}
            sx={{ p: 0.25 }}
          >
            {collapsed ? (
              <ChevronRightIcon fontSize="small" />
            ) : (
              <ExpandMoreIcon fontSize="small" />
            )}
          </IconButton>
        ) : null}
      </Box>
      {row.depth > 1 ? (
        <SubdirectoryArrowRightIcon
          fontSize="inherit"
          sx={{ color: 'text.disabled', flexShrink: 0, mr: 0.5 }}
        />
      ) : null}
      {row.task.is_milestone ? (
        <FlagOutlinedIcon
          fontSize="inherit"
          sx={{ color: 'warning.main', flexShrink: 0, mr: 0.5 }}
        />
      ) : null}
      {row.task.owner ? (
        <Box sx={{ mr: 0.5, display: 'flex', flexShrink: 0 }}>
          <UserAvatar user={row.task.owner} size={18} />
        </Box>
      ) : null}
      <Tooltip title={row.task.name}>
        <Box
          role="button"
          tabIndex={0}
          aria-label={`打开任务 ${row.task.name}`}
          onClick={() => onOpenTask(row.task.id)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              onOpenTask(row.task.id);
            }
          }}
          sx={{
            minWidth: 0,
            flex: 1,
            cursor: 'pointer',
            py: 0.5,
            '&:focus-visible': { outline: '2px solid', outlineColor: 'warning.main' },
          }}
        >
          <Typography variant="body2" noWrap sx={{ fontWeight: 400 }}>
            {row.task.name}
          </Typography>
        </Box>
      </Tooltip>
      {edit.enabled && row.task.can_edit ? <Tooltip title="编辑日期">
        <IconButton size="small" aria-label={`编辑日期 ${row.task.name}`} disabled={edit.busy}
          onClick={(event) => edit.begin(row.task, row.id, 'move', event.currentTarget, true)} sx={{ p: 0.25 }}>
          <EditCalendarOutlinedIcon sx={{ fontSize: 16 }} />
        </IconButton>
      </Tooltip> : null}
      {edit.enabled ? <Tooltip title={row.task.has_children ? '进度由下级汇总' : row.task.status === 'cancelled' ? '已取消任务请在详情中恢复' : !row.task.can_edit ? '任务只读' : '编辑进度'}>
        <Box component="span"><Box component="button" type="button" aria-label={`编辑进度 ${row.task.name}`}
          disabled={edit.busy || !canEditProgress(row.task)}
          onClick={(event) => edit.begin(row.task, row.id, 'progress', event.currentTarget, true)}
          sx={{ border: 0, bgcolor: 'transparent', color: 'text.secondary', font: 'inherit', fontSize: 12, cursor: 'pointer', p: 0.5 }}>
          {row.task.progress}%
        </Box></Box>
      </Tooltip> : <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0, ml: 0.5 }}>{row.task.progress}%</Typography>}
    </Box>
  );
}

export default function GanttChart({
  rows,
  edit,
  scale,
  labelHeader,
  today,
  collapsedIds,
  onToggleRow,
  onOpenTask,
  onOpenProject,
  activeTaskId = null,
  emptyHint = '当前选择下没有可展示的任务。',
}: GanttChartProps) {
  const theme = useTheme();
  const scrollRef = React.useRef<HTMLDivElement | null>(null);
  const finePointer = useMediaQuery('(pointer: fine)');
  const [viewport, setViewport] = React.useState({ start: 0, end: 0 });
  const measure = React.useCallback(() => {
    const node = scrollRef.current;
    if (node) setViewport({ start: node.scrollLeft, end: node.scrollLeft + Math.max(0, node.clientWidth - LABEL_WIDTH) });
  }, []);
  React.useEffect(() => {
    const node = scrollRef.current;
    if (!node) return;
    measure();
    const observer = new ResizeObserver(measure); observer.observe(node);
    return () => observer.disconnect();
  }, [measure, rows.length]);
  const priorWindow = React.useRef<{ start: string; scale: GanttScale } | null>(null);

  const window = React.useMemo(() => {
    const dates = rows.flatMap(rowDates);
    return buildGanttWindow(dates, scale, today);
  }, [rows, scale, today]);

  const windowKey = `${window.start.format('YYYY-MM-DD')}:${window.end.format('YYYY-MM-DD')}:${scale}`;

  // 时间窗口变化时把"今天"带到视野内；但只有今天确实在首屏之外才滚动。
  // 周/月尺度通常整张图能放下，强行居中反而会把当前月份的表头推到左侧标签列后面。
  React.useEffect(() => {
    const node = scrollRef.current;
    if (!node) return;
    const start = window.start.format('YYYY-MM-DD');
    const previous = priorWindow.current;
    priorWindow.current = { start, scale };
    if (previous && previous.scale === scale) {
      node.scrollLeft += window.start.diff(previous.start, 'day') * -window.dayWidth;
      measure(); return;
    }
    if (window.todayIndex === null) { measure(); return; }
    const viewportWidth = Math.max(0, node.clientWidth - LABEL_WIDTH);
    const todayOffset = window.todayIndex * window.dayWidth;
    node.scrollLeft =
      todayOffset < viewportWidth * 0.75
        ? 0
        : Math.max(0, todayOffset - viewportWidth * 0.35);
    measure();
  }, [windowKey, window.dayWidth, window.todayIndex, window.start, scale, measure]);

  const headerHeight = scale === 'month' ? 40 : 70;
  const monthBandHeight = scale === 'month' ? 40 : 32;
  const gridSize = Math.max(
    24,
    (scale === 'day' ? 1 : scale === 'week' ? 7 : 30) * window.dayWidth,
  );
  const todayLeft = window.todayIndex === null ? null : window.todayIndex * window.dayWidth;

  if (rows.length === 0) {
    return (
      <Box
        sx={{
          height: 240,
          border: 1,
          borderColor: 'divider',
          borderRadius: 1,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Typography variant="body2" color="text.secondary">
          {emptyHint}
        </Typography>
      </Box>
    );
  }

  return (
    <Box
      ref={scrollRef}
      data-gantt-scroll
      onScroll={measure}
      sx={{
        overflow: 'auto',
        // 比初版高约 100px：成员泳道/任务多时优先让图自己滚，避免只看得到 8–9 行
        maxHeight: 'clamp(480px, calc(100dvh - 300px), 960px)',
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
        // 横向滚动条出现时不要把标签列顶掉
        scrollbarGutter: 'stable',
      }}
    >
      <Box sx={{ minWidth: LABEL_WIDTH + window.width }}>
        {/* 表头：左侧标签与右侧时间轴都 sticky，纵向滚动时保持可见 */}
        <Box
          sx={{
            display: 'flex',
            position: 'sticky',
            top: 0,
            zIndex: 5,
            backgroundColor: 'background.paper',
            borderBottom: 1,
            borderColor: 'divider',
          }}
        >
          <Box
            sx={{
              width: LABEL_WIDTH,
              flexShrink: 0,
              position: 'sticky',
              left: 0,
              zIndex: 6,
              display: 'flex',
              alignItems: 'center',
              px: 1.5,
              height: headerHeight,
              backgroundColor: 'background.paper',
              borderRight: 1,
              borderColor: 'divider',
            }}
          >
            <Typography variant="subtitle2" noWrap>
              {labelHeader}
            </Typography>
          </Box>

          <Box
            sx={{
              width: window.width,
              flexShrink: 0,
              height: headerHeight,
              position: 'relative',
              backgroundColor: 'background.paper',
            }}
          >
            {window.months.map((month) => (
              <Box
                key={month.key}
                sx={{
                  position: 'absolute',
                  top: 0,
                  left: month.startIndex * window.dayWidth,
                  width: month.days * window.dayWidth,
                  height: monthBandHeight,
                  px: 0.75,
                  display: 'flex',
                  alignItems: 'center',
                  borderRight: 1,
                  borderBottom: scale === 'month' ? 0 : 1,
                  borderColor: 'divider',
                  overflow: 'hidden',
                }}
              >
                <Typography variant="caption" noWrap sx={{ fontWeight: 600 }}>
                  {month.days * window.dayWidth >= 88
                    ? month.label
                    : month.label.replace(/^\d{4}年/, '')}
                </Typography>
              </Box>
            ))}

            {window.ticks.map((tick) => {
              const tickWidth =
                scale === 'week'
                  ? Math.min(7, window.days - tick.index) * window.dayWidth
                  : window.dayWidth;
              return (
                <Box
                  key={tick.index}
                  sx={{
                    position: 'absolute',
                    top: monthBandHeight,
                    left: tick.index * window.dayWidth,
                    width: tickWidth,
                    height: headerHeight - monthBandHeight,
                    px: 0.5,
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    justifyContent: 'center',
                    borderRight: 1,
                    borderColor: 'divider',
                    overflow: 'hidden',
                  }}
                >
                  <Typography
                    variant="caption"
                    noWrap
                    sx={{ fontSize: '0.7rem', lineHeight: 1.2, fontVariantNumeric: 'tabular-nums' }}
                  >
                    {tick.label}
                  </Typography>
                  {tick.sublabel ? (
                    <Typography
                      variant="caption"
                      noWrap
                      sx={{ fontSize: '0.65rem', lineHeight: 1.2, color: 'text.secondary' }}
                    >
                      {tick.sublabel}
                    </Typography>
                  ) : null}
                </Box>
              );
            })}

            {todayLeft !== null ? (
              <Tooltip title="今天">
                <Box
                  sx={{
                    position: 'absolute',
                    top: 0,
                    bottom: 0,
                    left: todayLeft,
                    width: 2,
                    backgroundColor: 'error.main',
                    zIndex: 4,
                  }}
                />
              </Tooltip>
            ) : null}
          </Box>
        </Box>

        {rows.map((row) => {
          const isGroup = isProjectGanttRow(row) || isMemberGanttRow(row);
          const collapsed = collapsedIds.has(row.id);
          return (
            <Box
              key={row.id}
              data-gantt-row={row.id}
              sx={{
                display: 'flex',
                height: ROW_HEIGHT,
                borderBottom: 1,
                borderColor: 'divider',
                backgroundColor: isGroup ? 'action.hover' : 'background.paper',
                '&:hover': { backgroundColor: isGroup ? 'action.selected' : 'action.hover' },
              }}
            >
              <Box
                sx={{
                  width: LABEL_WIDTH,
                  flexShrink: 0,
                  position: 'sticky',
                  left: 0,
                  zIndex: 2,
                  display: 'flex',
                  alignItems: 'center',
                  px: 1.5,
                  pr: 1,
                  backgroundColor: isGroup ? 'background.default' : 'background.paper',
                  borderRight: 1,
                  borderColor: 'divider',
                }}
              >
                {isProjectGanttRow(row) ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', minWidth: 0, width: '100%' }}>
                    <IconButton
                      size="small"
                      aria-label={collapsed ? '展开项目任务' : '收起项目任务'}
                      disabled={edit.busy}
                      onClick={() => onToggleRow(row.id)}
                      sx={{ p: 0.25 }}
                    >
                      {collapsed ? (
                        <ChevronRightIcon fontSize="small" />
                      ) : (
                        <ExpandMoreIcon fontSize="small" />
                      )}
                    </IconButton>
                    <Box
                      sx={{
                        width: 10,
                        height: 10,
                        borderRadius: '50%',
                        flexShrink: 0,
                        mr: 1,
                        backgroundColor: row.project.color ?? 'primary.main',
                      }}
                    />
                    <Box
                      role={onOpenProject ? 'button' : undefined}
                      tabIndex={onOpenProject ? 0 : undefined}
                      onClick={() => onOpenProject?.(row.project.id)}
                      onKeyDown={(event) => {
                        if (onOpenProject && (event.key === 'Enter' || event.key === ' ')) {
                          event.preventDefault();
                          onOpenProject(row.project.id);
                        }
                      }}
                      sx={{
                        minWidth: 0,
                        flex: 1,
                        cursor: onOpenProject ? 'pointer' : 'default',
                        '&:focus-visible': { outline: '2px solid', outlineColor: 'warning.main' },
                      }}
                    >
                      <Typography variant="body2" noWrap sx={{ fontWeight: 600 }}>
                        {row.project.name}
                      </Typography>
                    </Box>
                    <Typography variant="caption" color="text.secondary" sx={{ ml: 0.5 }}>
                      {row.project.progress}%
                    </Typography>
                    {onOpenProject ? (
                      <Tooltip title="打开项目详情">
                        <IconButton
                          size="small"
                          aria-label={`打开项目 ${row.project.name}`}
                          onClick={() => onOpenProject(row.project.id)}
                          sx={{ p: 0.25, ml: 0.25 }}
                        >
                          <OpenInNewIcon sx={{ fontSize: 16 }} />
                        </IconButton>
                      </Tooltip>
                    ) : null}
                  </Box>
                ) : isMemberGanttRow(row) ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', minWidth: 0, width: '100%' }}>
                    <IconButton
                      size="small"
                      aria-label={collapsed ? '展开成员任务' : '收起成员任务'}
                      disabled={edit.busy}
                      onClick={() => onToggleRow(row.id)}
                      sx={{ p: 0.25 }}
                    >
                      {collapsed ? (
                        <ChevronRightIcon fontSize="small" />
                      ) : (
                        <ExpandMoreIcon fontSize="small" />
                      )}
                    </IconButton>
                    <Box sx={{ mr: 1, display: 'flex', flexShrink: 0 }}>
                      <UserAvatar
                        user={{
                          id: row.memberId,
                          display_name: row.displayName,
                          avatar_updated_at: row.avatarUpdatedAt,
                        }}
                        size={22}
                      />
                    </Box>
                    <Box
                      role="button"
                      tabIndex={0}
                      onClick={() => onToggleRow(row.id)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault();
                          onToggleRow(row.id);
                        }
                      }}
                      sx={{
                        minWidth: 0,
                        flex: 1,
                        cursor: 'pointer',
                        '&:focus-visible': { outline: '2px solid', outlineColor: 'warning.main' },
                      }}
                    >
                      <Typography variant="body2" noWrap sx={{ fontWeight: 600 }}>
                        {row.displayName}
                      </Typography>
                    </Box>
                    <Typography variant="caption" color="text.secondary" sx={{ ml: 0.5 }}>
                      {row.taskCount} 项 · {row.progress}%
                    </Typography>
                  </Box>
                ) : isTaskGanttRow(row) ? (
                  <Box
                    sx={{
                      width: '100%',
                      pl: Math.max(0, row.depth - 1) * 1.5,
                    }}
                  >
                    <TaskRowLabel
                      row={row}
                      edit={edit}
                      collapsed={collapsed}
                      onToggle={() => onToggleRow(row.id)}
                      onOpenTask={onOpenTask}
                    />
                  </Box>
                ) : null}
              </Box>

              <Box
                sx={{
                  width: window.width,
                  flexShrink: 0,
                  position: 'relative',
                  backgroundImage: `linear-gradient(to right, ${theme.palette.divider} 1px, transparent 1px)`,
                  backgroundSize: `${gridSize}px 100%`,
                }}
              >
                {todayLeft !== null ? (
                  <Box
                    aria-hidden
                    sx={{
                      position: 'absolute',
                      top: 0,
                      bottom: 0,
                      left: todayLeft,
                      width: 2,
                      backgroundColor: 'error.main',
                      opacity: 0.55,
                    }}
                  />
                ) : null}

                {isProjectGanttRow(row)
                  ? (() => {
                      const geometry = barGeometry(
                        row.project.start_date,
                        row.project.end_date,
                        window,
                      );
                      if (!geometry.visible) {
                        return null;
                      }
                      const color = row.project.color ?? theme.palette.primary.main;
                      return (
                        <Tooltip
                          title={`${row.project.name}：${formatNaturalDate(row.project.start_date)} ~ ${formatNaturalDate(row.project.end_date)}`}
                        >
                          <Box
                            component={onOpenProject ? 'button' : 'div'}
                            type={onOpenProject ? 'button' : undefined}
                            aria-label={onOpenProject ? `打开项目 ${row.project.name}` : undefined}
                            onClick={() => onOpenProject?.(row.project.id)}
                            sx={{
                              position: 'absolute',
                              left: geometry.left,
                              top: 14,
                              width: geometry.width,
                              height: 14,
                              borderRadius: 7,
                              border: 0,
                              p: 0,
                              backgroundColor: color,
                              cursor: onOpenProject ? 'pointer' : 'default',
                              overflow: 'hidden',
                              display: 'flex',
                              alignItems: 'center',
                            }}
                          >
                            {geometry.width >= 120 ? (
                              <Typography
                                variant="caption"
                                noWrap
                                sx={{
                                  px: 0.75,
                                  color: 'common.white',
                                  fontSize: '0.7rem',
                                  lineHeight: 1,
                                }}
                              >
                                {row.project.name}
                              </Typography>
                            ) : null}
                          </Box>
                        </Tooltip>
                      );
                    })()
                  : null}

                {isMemberGanttRow(row)
                  ? (() => {
                      const geometry = barGeometry(row.startDate, row.endDate, window);
                      if (!geometry.visible) {
                        return null;
                      }
                      return (
                        <Tooltip title={`${row.displayName} 的工作范围`}>
                          <Box
                            aria-hidden
                            sx={{
                              position: 'absolute',
                              left: geometry.left,
                              top: 18,
                              width: geometry.width,
                              height: 6,
                              borderRadius: 3,
                              backgroundColor: 'text.disabled',
                              opacity: 0.45,
                            }}
                          />
                        </Tooltip>
                      );
                    })()
                  : null}

                {isTaskGanttRow(row) ? (
                  <GanttTaskBar
                    task={row.task}
                    rowId={row.id}
                    edit={edit}
                    scrollRef={scrollRef}
                    viewport={viewport}
                    finePointer={finePointer}
                    window={window}
                    active={row.task.id === activeTaskId}
                    onOpenTask={onOpenTask}
                  />
                ) : null}
              </Box>
            </Box>
          );
        })}
      </Box>
    </Box>
  );
}
