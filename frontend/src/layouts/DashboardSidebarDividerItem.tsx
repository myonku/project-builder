// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/components/DashboardSidebarDividerItem.tsx
// Modified: 相对导入改为同层

import * as React from 'react';
import Divider from '@mui/material/Divider';
import type {} from '@mui/material/themeCssVarsAugmentation';
import DashboardSidebarContext from './DashboardSidebarContext';
import getDrawerSxTransitionMixin from './mixins';

export default function DashboardSidebarDividerItem() {
  const sidebarContext = React.useContext(DashboardSidebarContext);
  if (!sidebarContext) {
    throw new Error('Sidebar context was used without a provider.');
  }
  const { fullyExpanded = true, hasDrawerTransitions } = sidebarContext;

  return (
    <li>
      <Divider
        sx={[
          {
            borderBottomWidth: 1,
            my: 1,
            mx: -0.5,
          },
          hasDrawerTransitions
            ? getDrawerSxTransitionMixin(fullyExpanded, 'margin')
            : null,
        ]}
      />
    </li>
  );
}
