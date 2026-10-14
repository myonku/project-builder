/**
 * UUID 工具。
 *
 * **前端不生成业务 id**（id 由服务端生成），这里只做格式校验：
 * 用于路由参数、筛选输入框这类"提交前先挡掉明显非法输入"的场景，
 * 真正的合法性判定仍在后端（非法 UUID 会退化为 404 或 400）。
 */

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: unknown): value is string {
  return typeof value === 'string' && UUID_PATTERN.test(value);
}
