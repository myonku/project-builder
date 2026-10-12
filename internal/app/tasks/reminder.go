package tasks

import (
	"context"
)

// ReminderRecorder 是本模块对"记录一条提醒"的需求，由装配点用 app/reminders 实现。
//
// 与 ProjectAccess 同一模式：消费者定义接口，装配点做适配，
// 于是 tasks 与 reminders 之间不产生 import 依赖。
//
// 方法**不返回错误**，这是刻意的：提醒是任务的副作用，任务本身已经写库成功，
// 因为一封提醒没排上队就让用户的保存操作失败，是把次要功能的失败
// 放大成主要功能的失败。适配器内部负责记录日志。
//
// 需求文档 §9 同时要求"邮件发送不得阻塞用户操作"：因此这里只要求**插入一行**，
// 真正的 SMTP 发送由 reminders 的后台派发循环完成。
type ReminderRecorder interface {
	// RecordAssignment 记录"某人被加入任务"。
	RecordAssignment(ctx context.Context, taskID, userID string)
	// RecordOwnerChange 记录"任务持有者变更"。
	RecordOwnerChange(ctx context.Context, taskID, userID string)
}

// recordReminder 在提醒记录器存在时调用它。
//
// 集中一处做 nil 判断：未注入时（例如单元测试装配）静默跳过，
// 而不是让每个调用点各写一遍判空。
func (s *Service) recordReminder(
	_ context.Context, record func(ReminderRecorder),
) {
	if s.reminders == nil {
		return
	}
	record(s.reminders)
}
