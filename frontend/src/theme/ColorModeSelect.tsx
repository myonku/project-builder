// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/shared-theme/ColorModeSelect.tsx
// Modified: 未修改

import { useColorScheme } from '@mui/material/styles';
import MenuItem from '@mui/material/MenuItem';
import Select, { type SelectProps } from '@mui/material/Select';

export default function ColorModeSelect(props: SelectProps) {
  const { mode, setMode } = useColorScheme();
  if (!mode) {
    return null;
  }
  return (
    <Select
      value={mode}
      onChange={(event) =>
        setMode(event.target.value as 'system' | 'light' | 'dark')
      }
      SelectDisplayProps={{
        // @ts-ignore
        'data-screenshot': 'toggle-mode',
      }}
      {...props}
    >
      <MenuItem value="system">System</MenuItem>
      <MenuItem value="light">Light</MenuItem>
      <MenuItem value="dark">Dark</MenuItem>
    </Select>
  );
}
