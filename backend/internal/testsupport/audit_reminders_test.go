package testsupport

import (
	"context"
	"errors"
	"testing"
)

// TestAuditCoversReminderRetry 覆盖提醒域**唯一带操作人**的写路径：手动重试。
//
// 后台派发（sent / failed / skipped）刻意不入审计：它由周期循环产生，
// 没有操作人，而审计表的 actor_id 是必填外键——把收件人或任务持有者
// 当成操作人会记下"一个人没做过的事"。派发结果记在 reminders 表里
// （status / last_error / attempts），通过 GET /reminders 可查。
func TestAuditCoversReminderRetry(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "audit_reminder_owner")
	groupID := harness.CreateGroup(t, owner, "审计提醒组")
	projectID := harness.CreateProject(t, owner, groupID, "审计提醒项目")
	harness.CreateTask(t, owner, projectID, "审计提醒任务", "2026-09-28", "2026-10-05")

	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	// 让派发失败，从而得到一条可重试的 failed 提醒。
	harness.Mail.FailWith(errors.New("SMTP 连接超时"))
	if _, err := harness.Reminders.Dispatch(context.Background()); err != nil {
		t.Fatalf("Dispatch 返回错误: %v", err)
	}
	harness.Mail.FailWith(nil)

	var reminderID string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT id FROM reminders WHERE status = 'failed' LIMIT 1`).Scan(&reminderID); err != nil {
		t.Fatalf("查询失败提醒失败: %v", err)
	}

	// 派发本身不留审计记录（只有人工动作才入账）。
	if rows := auditRowsFor(t, harness, reminderID); len(rows) != 0 {
		t.Fatalf("后台派发不应写入审计记录，实际 %d 条：%+v", len(rows), rows)
	}

	owner.Client.Post(URL("/reminders/"+reminderID+"/retry"), map[string]any{}).
		RequireStatus(t, 200)

	rows := auditRowsFor(t, harness, reminderID)
	retried := requireAuditRow(t, rows, "updated", "manual_retry")
	if retried.ActorID != owner.UserID {
		t.Fatalf("重试记录的 actor_id = %s，期望 %s", retried.ActorID, owner.UserID)
	}

	// 负向断言：已发送（sent）的提醒不允许重试，因此不留记录。
	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	if _, err := harness.Reminders.Dispatch(context.Background()); err != nil {
		t.Fatalf("Dispatch 返回错误: %v", err)
	}

	var sentID string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT id FROM reminders WHERE status = 'sent' LIMIT 1`).Scan(&sentID); err != nil {
		t.Fatalf("查询已发送提醒失败: %v", err)
	}
	before := countAuditRowsFor(t, harness, sentID)
	owner.Client.Post(URL("/reminders/"+sentID+"/retry"), map[string]any{}).
		RequireStatus(t, 409)
	if after := countAuditRowsFor(t, harness, sentID); after != before {
		t.Fatalf("被拒的重试留下了 %d 条新记录（期望 0）", after-before)
	}
}
