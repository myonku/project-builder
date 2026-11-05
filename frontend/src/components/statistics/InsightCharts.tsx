import { Box, Button, Stack, Typography } from '@mui/material';
import { useTheme } from '@mui/material/styles';
export interface ChartDatum { label: string; value: number | null; second?: number; key?: string }
/** Shared count scale, explicit labels, integer ticks and native keyboard-operable bars. */
export function ColumnChart({data, label = '任务数', secondLabel, onSelect, height = 240}: {
 data: ChartDatum[]; label?: string; secondLabel?: string; onSelect?: (index: number) => void; height?: number;
}) {
 const max = Math.max(1, ...data.flatMap(v => [v.value ?? 0, v.second ?? 0]));
 const ticks = [...new Set([0, Math.ceil(max / 2), max])];
 return <Stack className="count-chart" spacing={1}>
  <Typography variant="caption" color="text.secondary">单位：个 · 蓝色：{label}{secondLabel ? ` · 绿色：${secondLabel}` : ''}</Typography>
  {!data.length ? <Typography color="text.secondary">当前范围没有数据。</Typography> : <Box className="count-chart-body" sx={{display: 'flex', gap: 1, minWidth: 0}}>
   <Stack className="count-chart-axis" sx={{height, width: 24, flexShrink: 0, justifyContent: 'space-between', pb: '46px', boxSizing: 'border-box'}}>
    {[...ticks].reverse().map(tick => <Typography key={tick} variant="caption">{tick}</Typography>)}
   </Stack>
   <Box className="count-chart-columns" sx={{display: 'grid', gridTemplateColumns: `repeat(${data.length}, minmax(0, 1fr))`, width: '100%', height, borderBottom: 1, borderColor: 'divider'}}>
    {data.map((item, i) => <Box className="count-chart-category" key={item.key ?? `${item.label}:${i}`} component={onSelect ? Button : 'div'}
     {...(onSelect ? {onClick: () => onSelect(i), 'aria-label': `${item.label}，${label} ${item.value ?? '未知'}${secondLabel ? `，${secondLabel} ${item.second ?? 0}` : ''}，查看明细`} : {})}
     sx={{minWidth: 0, p: 0.5, display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', color: 'text.primary', textTransform: 'none', borderRadius: 0}}>
     <Typography variant="caption" sx={{whiteSpace: 'nowrap'}}>{item.value === null ? '—' : item.value}{secondLabel && item.value !== null ? ` / ${item.second ?? 0}` : ''}</Typography>
     <Box className="count-chart-plot" sx={{display: 'flex', alignItems: 'flex-end', justifyContent: 'center', height: height - 84, width: '100%', gap: 0.4}}>
      <Box sx={{width: secondLabel ? '36%' : '65%', height: `${(item.value ?? 0) / max * 100}%`, minHeight: item.value ? 2 : 0, bgcolor: 'primary.main', borderRadius: '3px 3px 0 0'}} />
      {secondLabel && <Box sx={{width: '36%', height: `${(item.second ?? 0) / max * 100}%`, minHeight: item.second ? 2 : 0, bgcolor: 'success.main', borderRadius: '3px 3px 0 0'}} />}
     </Box>
     <Typography variant="caption" sx={{minHeight: 42, overflowWrap: 'anywhere', textAlign: 'center', lineHeight: 1.4, mt: 0.5}}>{item.label}</Typography>
    </Box>)}
   </Box>
  </Box>}
 </Stack>;
}
export function TrendChart({data}: {data: ChartDatum[]}) {
 const theme = useTheme(); const color = (theme.vars || theme).palette.primary.main;
 const max = Math.max(1, ...data.map(v => v.value ?? 0));
 const point = (i: number, value: number) => `${35 + i * 610 / Math.max(1, data.length - 1)},${180 - value / max * 150}`;
 const segments: string[] = []; let segment: string[] = [];
 data.forEach((v, i) => {if (v.value === null) {if (segment.length) segments.push(segment.join(' '));segment = [];} else segment.push(point(i, v.value));});
 if (segment.length) segments.push(segment.join(' '));
 return <Stack spacing={1}><Typography variant="caption" color="text.secondary">单位：个 · 未完成存量；未知区间断开，不补 0</Typography>
  <Box component="svg" viewBox="0 0 680 220" role="img" aria-label={data.map(v => `${v.label}：${v.value ?? '未知'}`).join('；')} sx={{width: '100%', maxHeight: 300}}>
   {[0, max].map(v => <g key={v}><line x1="35" x2="645" y1={180 - v / max * 150} y2={180 - v / max * 150} stroke="currentColor" opacity="0.2" /><text x="0" y={185 - v / max * 150} fill="currentColor" fontSize="16">{v}</text></g>)}
   {segments.map((points, i) => <polyline key={i} points={points} fill="none" stroke={color} strokeWidth="3" />)}
   {data.map((v, i) => v.value !== null && <circle key={i} cx={35 + i * 610 / Math.max(1, data.length - 1)} cy={180 - v.value / max * 150} r="4" fill={color}><title>{v.label}：{v.value}</title></circle>)}
   {data.filter((_, i) => i === 0 || i === data.length - 1 || i === Math.floor(data.length / 2)).map(v => <text key={v.label} x={35 + data.indexOf(v) * 610 / Math.max(1, data.length - 1)} y="210" textAnchor="middle" fill="currentColor" fontSize="16">{v.label}</text>)}
  </Box>
  <Typography variant="caption">{data.map(v => `${v.label} ${v.value ?? '—'}`).join(' · ')}</Typography>
 </Stack>;
}
