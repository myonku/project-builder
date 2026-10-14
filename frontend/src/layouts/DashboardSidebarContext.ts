// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/context/DashboardSidebarContext.ts
// Modified: 未修改

import * as React from 'react';

const DashboardSidebarContext = React.createContext<{
  onPageItemClick: (id: string, hasNestedNavigation: boolean) => void;
  mini: boolean;
  fullyExpanded: boolean;
  fullyCollapsed: boolean;
  hasDrawerTransitions: boolean;
} | null>(null);

export default DashboardSidebarContext;
