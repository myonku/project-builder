// Package mail 定义统一的邮件发送接口与 SMTP 实现。
//
// 定位（需求文档 §9）："首期使用 Outlook 官方 SMTP，地址、账号、发件人等
// 通过配置文件或环境变量注入，**业务层通过统一的邮件发送接口隔离具体实现**"。
//
// 因此本包只回答"怎么把一封信投出去"，不知道提醒、任务、项目是什么。
// 业务层（app/reminders）负责决定发给谁、发什么内容。
//
// 这一层存在的实际价值：测试不需要连真实 SMTP（注入替身即可），
// 而换供应商（或改成内网 MTA）只需再实现一个 Sender。
package mail

import (
	"context"
	"errors"
)

// Message 是一封待发送的邮件。
//
// 只有纯文本正文：需求文档 5.7 要求"邮件至少包含项目名、任务名、负责人、日期、
// 状态、进度、任务链接和提醒原因"，纯文本足以表达，且不必引入模板引擎。
type Message struct {
	To      string
	ToName  string
	Subject string
	Body    string
}

// Sender 是邮件发送接口。
//
// 约定：Send 返回 nil 表示**服务端已接收**（而不是"对方已读"）。
// 任何非 nil 错误都会被业务层记录到提醒的 last_error 并计入重试次数。
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// Disabled 是邮件未启用时使用的发送器。
//
// 它返回 ErrDisabled 而不是 nil：调用方需要能区分"发出去了"与"根本没发"，
// 否则未启用邮件时所有提醒都会被记成"发送成功"，那是比失败更糟的假象。
type Disabled struct{}

// ErrDisabled 表示邮件功能未启用。
var ErrDisabled = errors.New("mail: 邮件功能未启用")

// Send 实现 Sender。
func (Disabled) Send(context.Context, Message) error { return ErrDisabled }
