// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/theme/customizations/sidebar.tsx
// Modified: 相对导入改为同层 ../themePrimitives（本项目只保留一份主题令牌）

import { type Theme, type Components } from '@mui/material/styles';
import { listSubheaderClasses } from '@mui/material/ListSubheader';
import { listItemButtonClasses } from '@mui/material/ListItemButton';
import { typographyClasses } from '@mui/material/Typography';

/* eslint-disable import/prefer-default-export */
export const sidebarCustomizations: Components<Theme> = {
  MuiDrawer: {
    styleOverrides: {
      root: ({ theme }) => ({
        [`& .${listSubheaderClasses.root}`]: {
          lineHeight: 3,
        },
        [`& .${listItemButtonClasses.root}`]: {
          '&.Mui-selected': {
            [`& .${typographyClasses.root}`]: {
              color: (theme.vars ?? theme).palette.text.primary,
            },
          },
        },
      }),
    },
  },
};
