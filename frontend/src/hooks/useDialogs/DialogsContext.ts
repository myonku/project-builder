// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/hooks/useDialogs/DialogsContext.ts
// Modified: 未修改

import * as React from 'react';
import type { OpenDialog, CloseDialog } from './useDialogs';

const DialogsContext = React.createContext<{
  open: OpenDialog;
  close: CloseDialog;
} | null>(null);

export default DialogsContext;
