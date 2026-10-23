import type { ReactNode } from 'react';
import Box from '@mui/material/Box';
import Divider from '@mui/material/Divider';

const PLAN_TABLE_HEIGHT = 'clamp(520px, calc(100dvh - 300px), 1200px)';

/** Shared plan table height and page ending for grids without pagination footers. */
export default function PlanTableSection({ children }: { children: ReactNode }) {
  return <Box sx={{ minWidth: 0, pb: 4 }}>
    <Box sx={{ height: PLAN_TABLE_HEIGHT, width: '100%', minWidth: 0, scrollbarGutter: 'stable' }}>{children}</Box>
    <Divider sx={{ mt: 2 }} />
  </Box>;
}
