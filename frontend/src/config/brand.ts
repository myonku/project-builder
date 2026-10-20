/**
 * 品牌常量与资产入口（Worklane，2026-10-20）。
 *
 * **只管理用户可见品牌**：不包含 Go module / 数据库名 / Cookie 名 / 包名——
 * 这些工程标识按 P01 §2.3 保持不变。所有展示层从本文件取名称与图片，
 * 避免 `AppBrand`、`DashboardLayout`、登录页各自硬编码。
 */

import worklaneMark from '@/assets/brand/worklane-mark.png';
import worklaneMark1x from '@/assets/brand/worklane-mark@1x.png';
import wiltWordmark from '@/assets/brand/wilt-wordmark.png';
import wiltWordmark1x from '@/assets/brand/wilt-wordmark@1x.png';

/** 产品名：主页面左上角、文档标题、邮件称呼 */
export const BRAND_NAME = 'Worklane';

/** 文字标题：登录/注册页与公开说明 */
export const BRAND_TAGLINE = 'Work Items & Layered Timelines';

/** W 图标；主用 2x，窄屏/低 DPR 用 1x */
export const BRAND_MARK = worklaneMark;
export const BRAND_MARK_1X = worklaneMark1x;

/** WILT 字标；主用 2x，低 DPR 用 1x */
export const BRAND_WORDMARK = wiltWordmark;
export const BRAND_WORDMARK_1X = wiltWordmark1x;
