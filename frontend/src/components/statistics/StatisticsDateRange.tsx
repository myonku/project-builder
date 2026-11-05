import * as React from 'react';
import { Box, Button, Stack } from '@mui/material';
import { DatePicker } from '@mui/x-date-pickers/DatePicker';
import DateLocalizationProvider from '@/components/common/DateLocalizationProvider';
import { dayjsToNaturalDate, naturalDateToDayjs } from '@/utils/datetime';
import { validDate } from './statisticsModel';

export default function StatisticsDateRange({from, to, onApply, period = false}: {
 from: string; to: string; onApply: (from: string, to: string) => void; period?: boolean;
}) {
 const [draft, setDraft] = React.useState({from, to});
 const [invalid, setInvalid] = React.useState({from: false, to: false});
 React.useEffect(() => {setDraft({from, to}); setInvalid({from: false, to: false});}, [from, to]);
 const bad = invalid.from || invalid.to || (!!draft.from && !validDate(draft.from)) || (!!draft.to && !validDate(draft.to))
  || (!!draft.from && !!draft.to && draft.from > draft.to) || (period && (!draft.from || !draft.to));
 return <DateLocalizationProvider><Box sx={{gridColumn: {xs: 'auto', sm: 'span 2'}, minWidth: 0}}>
  <Stack direction={{xs: 'column', sm: 'row'}} spacing={1} sx={{alignItems: 'flex-start'}}>
   {(['from', 'to'] as const).map((field, i) => <DatePicker key={field} label={period ? (i ? '周期结束' : '周期开始') : (i ? '截止（止）' : '截止（起）')}
    value={naturalDateToDayjs(draft[field])} format="YYYY-MM-DD"
    localeText={{fieldYearPlaceholder: () => 'YYYY', fieldMonthPlaceholder: () => 'MM', fieldDayPlaceholder: () => 'DD'}}
    onChange={value => {setDraft(v => ({...v, [field]: value?.isValid() ? dayjsToNaturalDate(value) ?? '' : 'invalid'}));}}
    onError={error => setInvalid(v => ({...v, [field]: !!error}))}
    slotProps={{textField: {size: 'small', fullWidth: true, error: bad, helperText: i && bad ? '请填写有效且顺序正确的日期范围' : undefined}}} />)}
   <Button variant="outlined" disabled={!!bad} onClick={() => onApply(draft.from, draft.to)} sx={{flexShrink: 0, minHeight: 40}}>应用</Button>
  </Stack>
 </Box></DateLocalizationProvider>;
}
