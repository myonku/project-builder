// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/components/DashboardSidebarHeaderItem.tsx
// Modified: 相对导入改为同层

import * as React from 'react';
import ListSubheader from '@mui/material/ListSubheader';
import type {} from '@mui/material/themeCssVarsAugmentation';
import DashboardSidebarContext from './DashboardSidebarContext';
import { DRAWER_WIDTH } from './constants';
import getDrawerSxTransitionMixin from './mixins';

export interface DashboardSidebarHeaderItemProps {
  children?: React.ReactNode;
}

export default function DashboardSidebarHeaderItem({
  children,
}: DashboardSidebarHeaderItemProps) {
  const sidebarContext = React.useContext(DashboardSidebarContext);
  if (!sidebarContext) {
    throw new Error('Sidebar context was used without a provider.');
  }
  const {
    mini = false,
    fullyExpanded = true,
    hasDrawerTransitions,
  } = sidebarContext;

  return (
    <ListSubheader
      sx={[
        {
          fontSize: 12,
          fontWeight: '600',
          height: mini ? 0 : 36,
          px: 1.5,
          py: 0,
          minWidth: DRAWER_WIDTH,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          zIndex: 2,
        },
        hasDrawerTransitions
          ? getDrawerSxTransitionMixin(fullyExpanded, 'height')
          : null,
      ]}
    >
      {children}
    </ListSubheader>
  );
}
