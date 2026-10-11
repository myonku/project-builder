package testsupport

import (
	"context"
	"log/slog"
	"sync"

	"github.com/myonku/project-builder/internal/app/reminders"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/mail"
)

// FakeSender 是测试用的邮件发送器：记录发出的邮件，不连任何网络。
//
// 这是"发送模块分离"最直接的回报：提醒的全部逻辑（生成、资格判定、派发、
// 失败重试）都能在没有 SMTP 的情况下验证，且可以按需注入失败。
type FakeSender struct {
	mu       sync.Mutex
	messages []mail.Message
	failErr  error
}

var _ mail.Sender = (*FakeSender)(nil)

// Send 实现 mail.Sender。
func (f *FakeSender) Send(_ context.Context, message mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failErr != nil {
		return f.failErr
	}
	f.messages = append(f.messages, message)
	return nil
}

// Messages 返回已发出的邮件副本。
func (f *FakeSender) Messages() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]mail.Message(nil), f.messages...)
}

// Reset 清空已发出的邮件。
func (f *FakeSender) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.messages = nil
}

// FailWith 让后续发送全部失败；传 nil 恢复正常。
func (f *FakeSender) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failErr = err
}

// ReminderRecorderAdapter 把 tasks 模块的提醒记录请求适配为 reminders 模块的调用。
//
// 与 cmd/api 中的同名适配器职责一致：让两个业务模块互不依赖。
// 错误只记日志，不让任务操作失败（与生产装配保持同一语义）。
type ReminderRecorderAdapter struct {
	Service *reminders.Service
	Logger  *slog.Logger
}

// RecordAssignment 实现 tasks.ReminderRecorder。
func (a ReminderRecorderAdapter) RecordAssignment(ctx context.Context, taskID, userID string) {
	if a.Service == nil {
		return
	}
	if err := a.Service.RecordEvent(ctx, taskID, userID, domain.ReminderKindAssigned); err != nil {
		a.Logger.Warn("记录新任务分配提醒失败", slog.Any("cause", err))
	}
}

// RecordOwnerChange 实现 tasks.ReminderRecorder。
func (a ReminderRecorderAdapter) RecordOwnerChange(ctx context.Context, taskID, userID string) {
	if a.Service == nil {
		return
	}
	if err := a.Service.RecordEvent(ctx, taskID, userID, domain.ReminderKindOwnerChanged); err != nil {
		a.Logger.Warn("记录持有者变更提醒失败", slog.Any("cause", err))
	}
}
