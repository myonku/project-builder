/**
 * 骨架期的占位内容。
 *
 * 每个视图的**路由、页面容器、面包屑**都已经就位，只有业务内容待补——
 * 这样"跑起来"之后，后续每个功能点都是往已有骨架里填，而不是重新搭结构。
 * 占位卡片里写清楚"要用哪些端点"，避免实现时再去翻文档。
 */

import Alert from '@mui/material/Alert';
import AlertTitle from '@mui/material/AlertTitle';
import Box from '@mui/material/Box';
import List from '@mui/material/List';
import ListItem from '@mui/material/ListItem';
import ListItemText from '@mui/material/ListItemText';
import Typography from '@mui/material/Typography';

export interface PlaceholderContentProps {
  /** 这个页面将来负责什么 */
  description: string;
  /** 计划使用的端点与契约条目，逐条列出便于对照文档 */
  endpoints?: string[];
}

export default function PlaceholderContent({
  description,
  endpoints = [],
}: PlaceholderContentProps) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <Alert severity="info" variant="outlined">
        <AlertTitle>骨架期占位页面</AlertTitle>
        <Typography variant="body2">{description}</Typography>
      </Alert>
      {endpoints.length > 0 ? (
        <Box>
          <Typography variant="subtitle2" sx={{ mb: 1 }}>
            计划对接的接口
          </Typography>
          <List dense>
            {endpoints.map((item) => (
              <ListItem key={item} disableGutters>
                <ListItemText
                  slotProps={{
                    primary: { variant: 'body2', sx: { fontFamily: 'monospace' } },
                  }}
                  primary={item}
                />
              </ListItem>
            ))}
          </List>
        </Box>
      ) : null}
    </Box>
  );
}
