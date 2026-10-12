// Package requestid 让 request id 沿 context 传到用例层。
//
// 为什么需要它：request id 由 HTTP 中间件生成，但它的消费者不止 HTTP 层——
// 审计记录要把"同一次请求产生的记录"串起来（activity_logs.request_id）。
// 用 context 传递是标准做法：它随一次请求流动，既不必把它塞进每一个用例参数里，
// 也不会像包级变量那样在并发请求之间串味。
//
// 这是**唯一**允许通过 context 传递的业务相关信息；其它值（当前用户、权限）
// 必须显式出现在函数签名里，否则"谁在操作"就变得不可追踪。
package requestid

import "context"

// contextKey 是本包专用的键类型，避免与其它包的 context 值冲突。
type contextKey struct{}

// With 把 request id 写入 context。空串等同于不写入（From 会返回空串）。
func With(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, id)
}

// From 读取 request id；未设置时返回空串。
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, ok := ctx.Value(contextKey{}).(string)
	if !ok {
		return ""
	}
	return value
}
