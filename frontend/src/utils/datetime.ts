/**
 * 时间工具（F01 §5.1）。
 *
 * **两条硬约定**：
 * 1. `updated_at` 带亚秒精度，回传时**必须原样**——`passThrough` 是唯一出口，
 *    组件里不允许 `dayjs(x).format()` 之后再回传（截断到秒会让原子 CAS 必然失败）；
 * 2. `start_date` / `end_date` 是 `YYYY-MM-DD` 自然日，**不要用 `Date` 对象**，
 *    时区换算会把日期带偏一天。
 */

import dayjs from 'dayjs';
import timezonePlugin from 'dayjs/plugin/timezone';
import utcPlugin from 'dayjs/plugin/utc';

dayjs.extend(utcPlugin);
dayjs.extend(timezonePlugin);

export const DATE_FORMAT = 'YYYY-MM-DD';
export const DATETIME_FORMAT = 'YYYY-MM-DD HH:mm';

/**
 * 展示用格式化（只读场景）。
 *
 * @param value UTC ISO 8601 字符串；`null` / `undefined` / `''` 返回 `fallback`
 * @param timeZone 目标时区（通常传当前用户的 `user.timezone`）；省略时用浏览器本地时区
 */
export function formatForDisplay(
  value: string | null | undefined,
  timeZone?: string,
  pattern: string = DATETIME_FORMAT,
  fallback = '—',
): string {
  if (!value) {
    return fallback;
  }
  const parsed = timeZone ? dayjs(value).tz(timeZone) : dayjs(value);
  return parsed.isValid() ? parsed.format(pattern) : fallback;
}

/**
 * 提交用：**直接返回原字符串**。
 *
 * 存在的意义是把"版本字段只能原样回传"变成一处显式调用，
 * 便于在 review 与 grep 时确认没有经过任何格式化。
 */
export function passThrough(value: string): string {
  return value;
}

/** `YYYY-MM-DD` 自然日：原样展示，不做任何时区换算 */
export function formatNaturalDate(value: string | null | undefined, fallback = '—'): string {
  return value && value.length > 0 ? value : fallback;
}

/** 按指定时区计算"今天"的 `YYYY-MM-DD`（与后端 `is_overdue` 口径一致） */
export function todayIn(timeZone?: string): string {
  const now = timeZone ? dayjs().tz(timeZone) : dayjs();
  return now.format(DATE_FORMAT);
}

/** 把 `YYYY-MM-DD` 字符串转成 dayjs 对象（**只用于日期组件边界**，见 F02 §3） */
export function naturalDateToDayjs(value: string | null | undefined) {
  return value ? dayjs(value, DATE_FORMAT) : null;
}

/** 把日期组件的结果转回 `YYYY-MM-DD` 字符串 */
export function dayjsToNaturalDate(value: dayjs.Dayjs | null | undefined): string | null {
  return value ? value.format(DATE_FORMAT) : null;
}

export { dayjs };
