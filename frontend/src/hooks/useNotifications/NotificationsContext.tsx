// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/hooks/useNotifications/NotificationsContext.tsx
// Modified: 未修改

import * as React from 'react';
import type { ShowNotification, CloseNotification } from './useNotifications';

const NotificationsContext = React.createContext<{
  show: ShowNotification;
  close: CloseNotification;
} | null>(null);

export default NotificationsContext;
