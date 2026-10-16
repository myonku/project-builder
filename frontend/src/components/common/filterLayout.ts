/**
 * 筛选区的版式（项目列表 / 计划视图 / 提醒列表共用，F01 §5.10 第 8、9 条）。
 *
 * 抽成公共模块的原因：这几条规则是**踩出来的**，散落在每个页面里就会各自漂移：
 * 1. 行容器用 `Box` + `display:flex; flexWrap:wrap; gap`，**不要用 `Stack` 的 `spacing`**
 *    ——`spacing` 靠"容器负 margin + 子项正 margin"实现，折行后第二行起的左边缘会错位
 *    （F01 §6 第 30 条）；
 * 2. 需要"每一行都左右贴边"的那一层，项目用 `flex: 1 1 0` + `minWidth`（折行阈值）
 *    + `maxWidth`（兜住只剩一两项的残行，F01 §6 第 31 条）；
 * 3. 后几层（日期、开关+操作）内容通常占不满一行，用 `justifyContent: 'flex-end'` 靠右，
 *    避免右侧留一大片空白。
 */

import type { SxProps, Theme } from '@mui/material/styles';

/** 筛选区里的一行；`alignRight` 用于"内容占不满一行"的后几层 */
export function filterRowSx(alignRight = false): SxProps<Theme> {
  return {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 2,
    alignItems: 'center',
    justifyContent: alignRight ? 'flex-end' : 'flex-start',
  };
}

/**
 * 筛选区里的一项（放在需要"铺满整行"的那一层）。
 *
 * `minWidth` 决定**什么时候折行**（按各控件最长内容定）；`maxWidth` 默认 `2 × minWidth`，
 * 用来兜住"某一行只剩一两项"的情况——否则 `flex-grow` 会把单个控件拉成几百像素宽。
 * 需要更宽的残行时显式传 `maxWidth`。
 */
export function filterItemSx(minWidth: number, maxWidth = minWidth * 2): SxProps<Theme> {
  return { flex: '1 1 0', minWidth, maxWidth };
}
