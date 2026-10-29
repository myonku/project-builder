package testsupport

import (
	"context"
	"sync"

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
