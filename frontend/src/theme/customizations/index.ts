/**
 * 主题定制层的汇总入口。
 *
 * 本项目**只保留一份**主题层（F01 §2.2 的集成策略）：
 * - 来自官方 `shared-theme/` 的五个文件：inputs / dataDisplay / feedback / navigation / surfaces；
 * - 来自官方 `crud-dashboard/` 的五个文件：dataGrid / datePickers / formInput / sidebar / button。
 *
 * 两个模板的 `themePrimitives` 已合并为 `src/theme/themePrimitives.ts` 一份，
 * 因此这里不会出现"同名令牌两份、取值不同"的问题。
 */

export { inputsCustomizations } from './inputs';
export { dataDisplayCustomizations } from './dataDisplay';
export { feedbackCustomizations } from './feedback';
export { navigationCustomizations } from './navigation';
export { surfacesCustomizations } from './surfaces';

export { dataGridCustomizations } from './dataGrid';
export { datePickersCustomizations } from './datePickers';
export { formInputCustomizations } from './formInput';
export { sidebarCustomizations } from './sidebar';
export { buttonCustomizations } from './button';
