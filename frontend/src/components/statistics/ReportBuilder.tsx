import * as React from 'react';
import { Alert, Box, Button, Checkbox, FormControlLabel, Stack, Switch, TextField, Typography } from '@mui/material';
import { getReport, getReportItems, type InsightQuery, type InsightReport } from '@/api/insights';
import { useInsights } from '@/hooks/useInsights';
import { describeApiError } from '@/hooks/useApiError';
import { formatForDisplay } from '@/utils/datetime';
import { ChartCard, TaskList } from './StatisticsCharts';
import { ColumnChart, TrendChart } from './InsightCharts';
import { asPlanTasks, topCategories } from './insightModel';
import { validDate } from './statisticsModel';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';

export default function ReportBuilder({query, title}: {query: InsightQuery; title: string}) {
 const key = JSON.stringify(query);
 const valid = !!query.from && !!query.to && validDate(query.from) && validDate(query.to) && query.from <= query.to;
 const result = useInsights(valid ? query : null, signal => getReport(query, signal), query.timezone);
 const [report, setReport] = React.useState<InsightReport | null>(null);
 const [present, setPresent] = React.useState(false); const [screen, setScreen] = React.useState(0);
 const [busy, setBusy] = React.useState(false); const [error, setError] = React.useState(''); const [notice, setNotice] = React.useState('');
 const [heading, setHeading] = React.useState(''); const [notes, setNotes] = React.useState('');
 const [support, setSupport] = React.useState(''); const [nextNotes, setNextNotes] = React.useState('');
 const [selected, setSelected] = React.useState<string[]>([]); const [trend, setTrend] = React.useState(false);
 const [metric, setMetric] = React.useState('completed'); const [cursor, setCursor] = React.useState('');
 const [taskId, setTaskId] = React.useState<string | null>(null);
 const printRef = React.useRef(false); const host = React.useRef<HTMLDivElement>(null);
 React.useEffect(() => {
  const clear = () => document.querySelector('.report-print-root')?.remove();
  const prepare = () => {
   clear(); if (!host.current) return;
   const root = document.createElement('div'); root.className = 'report-print-root';
   const copy = host.current.cloneNode(true) as HTMLElement;
   copy.classList.remove('report-presentation'); copy.querySelector('.report-controls')?.remove();
   root.append(copy); document.body.append(root);
  };
  window.addEventListener('beforeprint', prepare);window.addEventListener('afterprint', clear);
  return () => {clear();window.removeEventListener('beforeprint', prepare);window.removeEventListener('afterprint', clear);};
 }, []);
 React.useEffect(() => {setReport(null);setPresent(false);setScreen(0);setHeading('');setNotes('');setSupport('');setNextNotes('');setSelected([]);setCursor('');setTaskId(null);}, [key]);
 React.useEffect(() => {
  setReport(result.data);
  if (result.data) setSelected(result.data.achievements.slice(0, 3).map(task => task.id));
 }, [result.data]);
 React.useEffect(() => {
  if (!present) return;
  const handle = (event: KeyboardEvent) => {
   if (event.key === 'Escape') {setPresent(false);if (document.fullscreenElement) void document.exitFullscreen();}
   if (event.key === 'ArrowRight' || event.key === 'PageDown') {event.preventDefault();setScreen(v => Math.min(3, v + 1));}
   if (event.key === 'ArrowLeft' || event.key === 'PageUp') {event.preventDefault();setScreen(v => Math.max(0, v - 1));}
  };
  window.addEventListener('keydown', handle);return () => window.removeEventListener('keydown', handle);
 }, [present]);
 React.useEffect(() => {
  if (printRef.current && report) {printRef.current = false;const frame = requestAnimationFrame(() => window.print());return () => cancelAnimationFrame(frame);}
 }, [report]);
 // A separate read checks current permissions before presentation/printing, keeping every screen atomic.
 const authorize = async (action: 'present' | 'print') => {
  setBusy(true);setError('');
  try {const fresh = await getReport({...query, as_of: report?.as_of});setReport(fresh);
   if (action === 'present') {setPresent(true);setScreen(0);} else printRef.current = true;
  } catch (failure) {setReport(null);setPresent(false);setError(describeApiError(failure));} finally {setBusy(false);}
 };
 const detail = useInsights(report && !present ? {...query, metric, cursor, as_of: report.as_of} : null,
  signal => getReportItems({...query, as_of: report!.as_of}, metric, cursor, signal), query.timezone);
 const data = report;
 if (!valid) return <Alert severity="info">选择有效的周期日期后生成汇报。</Alert>;
 if (result.error || error) return <Alert severity="error" action={<Button onClick={() => {setError('');result.reload();}}>重新生成</Button>}>{error || describeApiError(result.error!)}</Alert>;
 if (!data) return <Typography>正在生成周期汇报…</Typography>;
 const allUnknown = data.coverage.coverage_start >= data.observed_until;
 const partial = !data.coverage.complete;
 const periodOngoing = data.filter.to! >= data.as_of.slice(0, 10);
 const mainTitle = heading || `${title} · ${data.filter.from} 至 ${data.filter.to} 周期汇报`;
 const size = Math.max(1, Math.ceil(data.series.length / 8));
 const groups = Array.from({length: Math.ceil(data.series.length / size)}, (_, i) => data.series.slice(i * size, (i + 1) * size));
 const points = groups.map(values => ({label: values[0].date.slice(5), key: values[0].date, value: values[0].date > formatForDisplay(data.observed_until, query.timezone, 'YYYY-MM-DD') || values[values.length - 1].date < formatForDisplay(data.coverage.coverage_start, query.timezone, 'YYYY-MM-DD') ? null : values.reduce((n, v) => n + v.created, 0), second: values.reduce((n, v) => n + v.completed, 0)}));
 const breakdown = topCategories((query.scope === 'group' ? data.members : data.projects).map(v => ({key: v.id, label: v.name, value: v.completed, second: v.open})), 6);
 const achievements = data.achievements.filter(task => selected.includes(task.id)).slice(0, 3);
 const split = (value: string, max = 3) => value.split('\n').map(v => v.trim()).filter(Boolean).slice(0, max);
 const manual = split(notes);
 const footer = `${data.filter.from} – ${data.filter.to} · ${query.timezone} · ${partial ? `部分记录，自 ${formatForDisplay(data.coverage.coverage_start, query.timezone)}` : '完整周期记录'} · 观察截止 ${formatForDisplay(data.observed_until, query.timezone)}`;
 const metricCards = [['本期完成', data.metrics.completed], ['本期新增', data.metrics.created], ['本期重开', data.metrics.reopened], ['期末未完成', data.metrics.open]];
 const panels = [
  <React.Fragment key="summary"><Typography component="h2" variant="h4">本期摘要</Typography>
   <Box className="report-numbers" sx={{display: 'grid', gridTemplateColumns: 'repeat(4,minmax(0,1fr))', gap: 2}}>
    {metricCards.map(([label, value]) => <Box key={label} sx={{borderBottom: 2, borderColor: 'primary.main', pb: 2}}><Typography>{label}{partial ? '（部分记录）' : ''}</Typography><Typography className="report-number" variant="h3">{allUnknown ? '—' : value}</Typography></Box>)}
   </Box>
   <Typography component="h3" variant="h5">代表成果</Typography>
   {achievements.map(task => <Typography key={task.id}>• {task.name}{task.status !== 'completed' ? '（期末已重开或调整）' : ''}</Typography>)}
   {manual.map((value, i) => <Typography key={i}>• {value}</Typography>)}
   {!achievements.length && !manual.length && <Typography color="text.secondary">本期暂无已记录成果，可在编辑区补充有依据的说明。</Typography>}
   {data.previous && <Typography>相较上一完整周期：完成数量差 {data.metrics.completed - data.previous.completed}；上期完成 {data.previous.completed}（不作效率归因）。</Typography>}
  </React.Fragment>,
  <React.Fragment key="rhythm"><Typography component="h2" variant="h4">本期推进节奏</Typography>
   {trend ? <TrendChart data={groups.map(values => ({label: values[0].date.slice(5), value: values[values.length - 1].open}))} /> : <ColumnChart data={points} label="新增" secondLabel="完成" height={present ? 320 : 240} />}
   <Typography>最多显示8段，较长周期合并相邻桶。任务跨日重开再完成可在不同桶各计一次，柱合计可能大于本期去重完成数。</Typography>
  </React.Fragment>,
  <React.Fragment key="distribution"><Typography component="h2" variant="h4">{query.scope === 'group' ? '成员分布与成果' : '项目分布与成果'}</Typography>
   <ColumnChart data={breakdown} label="本期完成" secondLabel="期末未完成" height={present ? 320 : 240} />
   <Typography>共 {query.scope === 'group' ? data.members.length : data.projects.length} 个分类，超过 6 项汇总为“其他”。成员为参与口径，多人任务分别计入，不代表工时或效率。</Typography>
   {achievements.slice(0, 2).map(task => <Typography key={task.id}>• {task.name}</Typography>)}
  </React.Fragment>,
  <React.Fragment key="next"><Typography component="h2" variant="h4">待关注与下期安排</Typography>
   <Typography component="h3" variant="h5">期末关注</Typography>
   {data.attention.slice(0, 3).map(task => <Typography key={task.id}>• {task.name} · 截止 {task.end_date}</Typography>)}
   {!data.attention.length && <Typography>本期可见记录中没有期末逾期事项。</Typography>}
   {split(support).map((v, i) => <Typography key={i}>协助事项：{v}</Typography>)}
   <Typography component="h3" variant="h5">下期安排（生成时最新计划）</Typography>
   {data.next.slice(0, 3).map(task => <Typography key={task.id}>• {task.name} · {task.end_date}{task.is_milestone ? ' · 里程碑' : ''}</Typography>)}
   {split(nextNotes).map((v, i) => <Typography key={i}>• {v}</Typography>)}
   {!data.next.length && !nextNotes && <Typography>下期暂无已记录安排，可补充下一步计划。</Typography>}
   <Typography>期末逾期共 {data.metrics.overdue} 项、下期最新安排共 {data.next.length} 项；此屏各显示最多 3 项。</Typography>
  </React.Fragment>,
 ];
 return <Stack spacing={2}>
  <Stack className="report-editor statistics-no-print" spacing={2}>
   <Alert severity={partial ? 'warning' : 'info'}>{periodOngoing ? `进行中，截至 ${formatForDisplay(data.observed_until, query.timezone)}。` : '已结束周期。'}{partial ? `历史记录不完整，起点为 ${formatForDisplay(data.coverage.coverage_start, query.timezone)}；未知区间不补0，环比不可用。` : data.policy}</Alert>
   <Stack direction="row" sx={{flexWrap: 'wrap', gap: 1}}><Button variant="contained" onClick={() => void authorize('present')} disabled={busy}>开始演示</Button><Button variant="outlined" onClick={() => void authorize('print')} disabled={busy}>打印周期汇报</Button><Button onClick={() => void navigator.clipboard.writeText([mainTitle, footer, data.policy, ...metricCards.map(([name, value]) => `${name}：${allUnknown ? '—' : value}${partial ? '（部分记录）' : ''}`), ...achievements.map(t => `成果：${t.name}`), notes, support, nextNotes].filter(Boolean).join('\n')).then(() => setNotice('已复制汇报摘要。')).catch(() => setNotice('复制失败，请检查浏览器剪贴板权限。'))}>复制汇报摘要</Button><Button onClick={result.reload} disabled={busy}>重新生成</Button><FormControlLabel control={<Switch checked={trend} onChange={(_, v) => setTrend(v)} />} label="推进页显示存量折线" /></Stack>
   <TextField label="汇报标题（会话草稿）" value={heading} onChange={e => setHeading(e.target.value.slice(0, 100))} />
   <TextField label="成果说明（每行一项，最多3项）" multiline minRows={2} value={notes} onChange={e => setNotes(e.target.value.slice(0, 600))} />
   <Box sx={{display: 'grid', gridTemplateColumns: {xs: '1fr', md: '1fr 1fr'}, gap: 2}}><TextField label="阻碍 / 需要协助（最多3行）" multiline minRows={2} value={support} onChange={e => setSupport(e.target.value.slice(0, 500))} /><TextField label="下期补充安排（最多3行）" multiline minRows={2} value={nextNotes} onChange={e => setNextNotes(e.target.value.slice(0, 500))} /></Box>
   <Box component="details"><Box component="summary">选择代表成果（最多3项，共 {data.achievements.length} 项候选）</Box>{data.achievements.map(task => <FormControlLabel key={task.id} label={task.name} control={<Checkbox checked={selected.includes(task.id)} disabled={!selected.includes(task.id) && selected.length >= 3} onChange={(_, checked) => setSelected(v => checked ? [...v, task.id] : v.filter(id => id !== task.id))} />} />)}</Box>
  </Stack>
  {notice && <Alert className="statistics-no-print" severity="info" onClose={() => setNotice('')}>{notice}</Alert>}
  <Box ref={host} className={`report-deck${present ? ' report-presentation' : ''}`}>
   {present && <Stack className="report-controls" direction="row" sx={{gap: 1, mb: 1, flexWrap: 'wrap'}}><Button onClick={() => {setPresent(false);if (document.fullscreenElement) void document.exitFullscreen();}}>退出演示（Esc）</Button><Button disabled={!screen} onClick={() => setScreen(v => v - 1)}>上一屏</Button><Button disabled={screen === 3} onClick={() => setScreen(v => v + 1)}>下一屏</Button><Button onClick={() => void host.current?.requestFullscreen().catch(() => setNotice('浏览器未允许全屏，仍可使用当前演示布局。'))}>全屏</Button><Button onClick={() => setTrend(v => !v)}>{trend ? '查看新增/完成' : '查看存量折线'}</Button></Stack>}
   {panels.map((panel, index) => <Box component="section" key={index} className={`report-sheet${index !== screen ? ' report-inactive' : ''}`}>
    <Typography className="report-title" component="h1" variant="h5">{mainTitle}</Typography>
    <Stack className="report-content" spacing={2}>{panel}</Stack>
    <Typography className="report-footer" variant="caption">{footer} · 第 {index + 1} / 4 屏</Typography>
   </Box>)}
  </Box>
  {!present && <Box className="statistics-no-print"><ChartCard title="核查周期指标明细">
   <Stack spacing={1}><Stack direction="row" sx={{gap: 1, flexWrap: 'wrap'}}>{[['completed','本期完成'],['created','本期新增'],['reopened','本期重开'],['open','期末未完成'],['overdue','期末逾期']].map(([value,label]) => <Button key={value} variant={metric === value ? 'contained' : 'text'} onClick={() => {setMetric(value);setCursor('');}}>{label}</Button>)}</Stack>
    {detail.error && <Alert severity="warning">{describeApiError(detail.error)}</Alert>}
    {detail.data && <><Typography>共 {detail.data.total} 条 · 历史指标明细，点击查看当前任务</Typography><TaskList key={`${metric}:${cursor}`} tasks={asPlanTasks(detail.data.items, result.today)} onOpen={setTaskId} />{detail.data.next_cursor && <Button onClick={() => setCursor(detail.data!.next_cursor)}>下一批明细</Button>}{cursor && <Button onClick={() => setCursor('')}>返回首批</Button>}</>}
   </Stack></ChartCard></Box>}
  <TaskDetailDrawer key={taskId ?? 'none'} taskId={taskId} onClose={() => setTaskId(null)} onChanged={() => {setTaskId(null);result.reload();}} />
 </Stack>;
}
