import * as React from 'react';
import { Alert, Box, Button, Checkbox, FormControlLabel, MenuItem, Stack, TextField, Typography } from '@mui/material';
import { useSearchParams } from 'react-router';
import PageContainer from '@/components/common/PageContainer';
import { getOverview, type InsightQuery } from '@/api/insights';
import { useInsights } from '@/hooks/useInsights';
import { useGroupOptions } from '@/hooks/useGroupOptions';
import { useAuthStore } from '@/stores/auth';
import { useUiStore } from '@/stores/ui';
import { todayIn } from '@/utils/datetime';
import StatisticsDateRange from '@/components/statistics/StatisticsDateRange';
import InsightOverview from '@/components/statistics/InsightOverview';
import ReportBuilder from '@/components/statistics/ReportBuilder';
import { isUUID, validDate } from '@/components/statistics/statisticsModel';
import { periodRange, type PeriodPreset } from '@/components/statistics/insightModel';

export default function StatisticsWorkspace({page}: {page: 'overview' | 'report'}) {
 const [params, setParams] = useSearchParams();
 const user = useAuthStore(state => state.user);
 const currentGroupId = useUiStore(state => state.currentGroupId);
 const groups = useGroupOptions();
 const scope = params.get('scope') ?? (params.get('tab') === 'members' ? 'member' : 'group');
 const groupId = params.get('groupId') ?? ''; const memberId = params.get('memberId') ?? ''; const projectId = params.get('projectId') ?? '';
 const timezone = params.get('timezone') ?? user?.timezone ?? 'UTC';
 const archived = params.get('includeArchived') === 'true';
 let zoneValid = true; try {new Intl.DateTimeFormat('en-US', {timeZone: timezone});} catch {zoneValid = false;}
 const today = todayIn(zoneValid ? timezone : 'UTC'); const defaults = periodRange('week', today);
 const from = params.get('from') ?? defaults.from; const to = params.get('to') ?? defaults.to;
 const dueFrom = params.get('dueFrom') ?? ''; const dueTo = params.get('dueTo') ?? '';
 const update = React.useCallback((values: Record<string, string>, replace = false) => {
  const next = new URLSearchParams(params);
  if (!next.has('scope') && next.get('tab') === 'members') next.set('scope', 'member');
  next.delete('tab'); next.delete('page');
  Object.entries(values).forEach(([key, value]) => value ? next.set(key, value) : next.delete(key));
  setParams(next, {replace});
 }, [params, setParams]);
 React.useEffect(() => {
  if (scope !== 'self' && !params.has('groupId') && groups.options.length) update({groupId: groups.options.find(group => group.id === currentGroupId)?.id ?? groups.options[0].id}, true);
 }, [scope, groups.options, params, currentGroupId, update]);
 const invalid = !['self', 'group', 'member'].includes(scope)
  || !zoneValid || (!!groupId && !isUUID(groupId)) || (!!memberId && !isUUID(memberId)) || (!!projectId && !isUUID(projectId))
  || (page === 'report' && (!validDate(from) || !validDate(to) || from > to))
  || (!!dueFrom && !validDate(dueFrom)) || (!!dueTo && !validDate(dueTo)) || (!!dueFrom && !!dueTo && dueFrom > dueTo);
 const query: InsightQuery = {scope: scope as InsightQuery['scope'], timezone, ...(scope !== 'self' ? {group_id: groupId} : {}),
  ...(scope === 'member' ? {member_id: memberId} : {}), ...(projectId ? {project_id: projectId} : {}), include_archived: archived};
 const optionsQuery: InsightQuery = {...query, scope: scope === 'member' ? 'group' : query.scope, member_id: undefined, project_id: undefined};
 const canRead = !invalid && (scope === 'self' || !!groupId);
 const options = useInsights(canRead ? optionsQuery : null, signal => getOverview(optionsQuery, signal), zoneValid ? timezone : 'UTC');
 const ready = canRead && (scope !== 'member' || !!memberId);
 const scopeName = scope === 'self' ? '本人承担' : scope === 'member'
  ? `${groups.nameById.get(groupId) ?? '指定组'} · ${options.data?.member_options.find(member => member.user_id === memberId)?.display_name ?? '指定成员'}`
  : `${groups.nameById.get(groupId) ?? '指定工作组'} · 共享计划`;
 const reportQuery: InsightQuery = {...query, from, to, bucket: params.get('bucket') === 'week' ? 'week' : 'day'};
 const title = page === 'report' ? '周期汇报' : '数据概览';
 return <PageContainer title={title} breadcrumbs={[{title: '工作台', path: '/'}, {title: '统计', path: `/statistics/overview?${params}`}, {title}]}>
  <Stack className="insights-workspace" spacing={2}>
   <Box className="report-workspace-controls" sx={{display: 'grid', gridTemplateColumns: {xs: 'minmax(0,1fr)', sm: 'repeat(2,minmax(0,1fr))', lg: 'repeat(4,minmax(0,1fr))'}, gap: 1.5}}>
    <TextField select label="查看范围" size="small" value={scope} onChange={e => update({scope: e.target.value, memberId: '', projectId: ''})}><MenuItem value="self">本人承担</MenuItem><MenuItem value="group">组内共享</MenuItem><MenuItem value="member">组内成员</MenuItem></TextField>
    {scope !== 'self' && <TextField select label="工作组" size="small" value={groupId} onChange={e => update({groupId: e.target.value, memberId: '', projectId: ''})} slotProps={{inputLabel: {shrink: true}, select: {displayEmpty: true}}}>
     {!groups.options.some(group => group.id === groupId) && <MenuItem value={groupId}>{groupId ? '指定工作组' : '请选择'}</MenuItem>}{groups.options.map(group => <MenuItem key={group.id} value={group.id}>{group.name}</MenuItem>)}
    </TextField>}
    {scope === 'member' && <TextField select label="成员" size="small" value={memberId} onChange={e => update({memberId: e.target.value})} slotProps={{inputLabel: {shrink: true}, select: {displayEmpty: true}}}>
     <MenuItem value="">请选择成员</MenuItem>{memberId && !options.data?.member_options.some(member => member.user_id === memberId) && <MenuItem value={memberId}>指定成员</MenuItem>}{options.data?.member_options.map(member => <MenuItem key={member.user_id} value={member.user_id}>{member.display_name}（{member.username}）</MenuItem>)}
    </TextField>}
    <TextField select label="项目" size="small" value={projectId} onChange={e => update({projectId: e.target.value})} slotProps={{inputLabel: {shrink: true}, select: {displayEmpty: true}}}>
     <MenuItem value="">全部项目</MenuItem>{projectId && !options.data?.project_options[projectId] && <MenuItem value={projectId}>指定项目</MenuItem>}{Object.entries(options.data?.project_options ?? {}).map(([id, name]) => <MenuItem key={id} value={id}>{name}</MenuItem>)}
    </TextField>
   </Box>
   <Box className="report-workspace-controls"><Typography variant="body2" color="text.secondary">{scopeName} · {page === 'report' ? '周期事实，保留当前可读归档项目的周期贡献' : '当前可见叶子任务，截止范围不代表历史完成期间'} · {timezone}</Typography>
    {page === 'report' ? <Stack spacing={1.5}>
     <Stack direction="row" sx={{gap: 1, flexWrap: 'wrap', mt: 1}}>{([['week','本周'],['lastWeek','上周'],['month','本月'],['lastMonth','上月']] as [PeriodPreset, string][]).map(([value,label]) => <Button key={value} variant="outlined" onClick={() => update({...periodRange(value, today), bucket: value.toLowerCase().includes('month') ? 'week' : 'day'})}>{label}</Button>)}</Stack>
     <StatisticsDateRange period from={from} to={to} onApply={(a, b) => update({from: a, to: b})} />
     <Stack direction="row" sx={{gap: 1, flexWrap: 'wrap'}}><TextField select label="分桶" value={reportQuery.bucket} onChange={e => update({bucket: e.target.value})} size="small"><MenuItem value="day">按日</MenuItem><MenuItem value="week">按自然周</MenuItem></TextField><TextField label="报告时区（IANA）" size="small" value={timezone} error={!zoneValid} onChange={e => update({timezone: e.target.value})} /></Stack>
    </Stack> : <Stack spacing={1}><FormControlLabel control={<Checkbox checked={archived} onChange={(_, value) => update({includeArchived: String(value)})} />} label="当前概览纳入归档" /><StatisticsDateRange from={dueFrom} to={dueTo} onApply={(a, b) => update({dueFrom: a, dueTo: b})} /></Stack>}
   </Box>
   {groups.error && <Alert severity="warning">工作组选项暂不可用。</Alert>}{invalid && <Alert severity="warning">查询条件无效，请检查范围、日期或报告时区。</Alert>}{!ready && !invalid && <Alert severity="info">请选择有权限的工作组和成员范围。</Alert>}
   {ready && (page === 'report' ? <ReportBuilder key={JSON.stringify(reportQuery)} query={reportQuery} title={scopeName} /> : <InsightOverview query={query} dueFrom={dueFrom} dueTo={dueTo} />)}
  </Stack>
 </PageContainer>;
}
