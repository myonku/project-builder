/**
 * 进度单元格：一条进度条 + 百分比文字，**专供 DataGrid 的 `renderCell` 使用**。
 *
 * 为什么单独做组件（F01 §6 第 22、28 条）：
 * DataGrid 的单元格**不是 flex 容器**，它靠 `line-height: 行高` 把**行内内容**垂直居中。
 * `CellText`（`<span>`）、`Chip`、`IconButton` 都是行内级，天然居中；
 * 而"进度条 + 文字"必须用块级容器排一行——块级元素不吃祖先的 `line-height`，
 * 会直接贴在单元格顶部（实测：进度条与 `0%` 都比同行内容高约 13px）。
 *
 * 解法写在组件里，调用方不用再记：**容器撑满单元格高度 + `alignItems: center`**。
 * 以后任何"单元格里要多行/多列布局"的地方都照这个写。
 */

import Box from '@mui/material/Box';
import LinearProgress from '@mui/material/LinearProgress';
import Stack from '@mui/material/Stack';
import CellText from '@/components/common/CellText';

export interface ProgressCellProps {
  /** 0–100；`null` / `undefined` 按 0 处理 */
  value: number | null | undefined;
  /** 数字列的宽度，默认 40（`100%` 也放得下） */
  labelWidth?: number;
  /** 进度条高度，默认 6 */
  barHeight?: number;
}

export default function ProgressCell({ value, labelWidth = 40, barHeight = 6 }: ProgressCellProps) {
  const percent = Math.min(100, Math.max(0, Math.round(value ?? 0)));

  return (
    <Stack
      direction="row"
      spacing={1}
      sx={{
        alignItems: 'center',
        // 关键：块级容器要自己撑满单元格，否则会被顶到顶部（见组件头注释）
        width: '100%',
        height: '100%',
      }}
    >
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <LinearProgress
          variant="determinate"
          value={percent}
          sx={{ height: barHeight, borderRadius: barHeight / 2 }}
        />
      </Box>
      <CellText muted sx={{ width: labelWidth, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
        {percent}%
      </CellText>
    </Stack>
  );
}
