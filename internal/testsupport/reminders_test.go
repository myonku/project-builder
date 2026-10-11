package testsupport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/app/reminders"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/mail"
)

// reminderDate 解析业务日期，供用例表达"今天/到期日"。
func reminderDate(t *testing.T, raw string) time.Time {
	t.Helper()

	parsed, err := time.Parse(domain.DateLayout, raw)
	if err != nil {
		t.Fatalf("解析日期 %q 失败: %v", raw, err)
	}
	return parsed
}

// TestReminderOverdueLifecycle 覆盖"延期提醒"的完整生命周期：
// 生成 → 派发 → 记录 → 查询 → 不重复。
//
// 用延期而不是"到期前"作为主用例：harness 的固定时钟是 2026-10-11（周日），
// 而"提前 1 个工作日"的规则决定了周日**不会**产生任何到期前提醒
// （周一到期的任务应在上周五提醒）。这本身就是规则正确性的一个体现，
// 因此到期前的场景用移动时钟单独覆盖。
func TestReminderOverdueLifecycle(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "rem_owner")
	groupID := harness.CreateGroup(t, owner, "提醒组")
	projectID := harness.CreateProject(t, owner, groupID, "提醒项目")
	// 到期日早于固定时钟（2026-10-11）→ 已延期。
	taskID := harness.CreateTask(t, owner, projectID, "已延期的任务", "2026-09-28", "2026-10-05")

	// 把任务推进成"进行中"，否则"未开始"的任务不该收到期提醒（该收的是开始前提醒）。
	version := Str(owner.Client.Get(URL("/tasks/"+taskID)).
		RequireStatus(t, http.StatusOK).Data(t), "updated_at")
	owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"status":              "in_progress",
		"progress":            40,
		"expected_updated_at": version,
	}).RequireStatus(t, http.StatusOK)

	// 创建任务时已记录了"新任务分配"提醒，这里先清掉以便聚焦延期提醒。
	harness.Mail.Reset()

	t.Run("生成延期提醒", func(t *testing.T) {
		report, err := harness.Reminders.Generate(context.Background())
		if err != nil {
			t.Fatalf("Generate 返回错误: %v", err)
		}
		if report.Generated == 0 {
			t.Fatal("应至少生成一条提醒")
		}

		var count int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM reminders WHERE task_id = $1 AND kind = 'task_overdue'`,
			taskID).Scan(&count); err != nil {
			t.Fatalf("统计提醒失败: %v", err)
		}
		if count != 1 {
			t.Fatalf("延期提醒数量 = %d，期望 1", count)
		}
	})

	t.Run("重复扫描不产生重复提醒", func(t *testing.T) {
		// 幂等键是唯一防线：周期性扫描必然会反复运行。
		for range 3 {
			if _, err := harness.Reminders.Generate(context.Background()); err != nil {
				t.Fatalf("Generate 返回错误: %v", err)
			}
		}
		var count int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM reminders WHERE task_id = $1 AND kind = 'task_overdue'`,
			taskID).Scan(&count); err != nil {
			t.Fatalf("统计提醒失败: %v", err)
		}
		if count != 1 {
			t.Fatalf("延期提醒数量 = %d，期望仍为 1（延期只提醒一次）", count)
		}
	})

	t.Run("派发并把邮件发给持有者", func(t *testing.T) {
		report, err := harness.Reminders.Dispatch(context.Background())
		if err != nil {
			t.Fatalf("Dispatch 返回错误: %v", err)
		}
		if report.Sent == 0 {
			t.Fatalf("应至少发送一封，报告 = %+v", report)
		}

		messages := harness.Mail.Messages()
		if len(messages) == 0 {
			t.Fatal("假发送器没有收到邮件")
		}
		message := messages[0]
		if !strings.Contains(message.Subject, "已延期") {
			t.Errorf("主题应说明提醒类型，实际 %q", message.Subject)
		}

		// 需求文档 5.7：邮件至少包含项目名、任务名、负责人、日期、状态、进度、
		// 任务链接和提醒原因。
		required := []string{
			"项目：提醒项目",
			"任务：已延期的任务",
			"负责人：",
			"计划周期：2026-09-28 ~ 2026-10-05",
			"状态：进行中",
			"进度：40%",
			"任务链接：https://example.test/tasks/" + taskID,
			"提醒原因：",
		}
		for _, fragment := range required {
			if !strings.Contains(message.Body, fragment) {
				t.Errorf("邮件正文缺少 %q，实际正文：\n%s", fragment, message.Body)
			}
		}

		// 状态必须写回为 sent，否则下一轮会重复发送。
		var status string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT status FROM reminders WHERE task_id = $1 AND kind = 'task_overdue'`,
			taskID).Scan(&status); err != nil {
			t.Fatalf("查询提醒状态失败: %v", err)
		}
		if status != domain.ReminderStatusSent {
			t.Fatalf("提醒状态 = %q，期望 sent", status)
		}
	})

	t.Run("再次派发不会重复发送", func(t *testing.T) {
		harness.Mail.Reset()
		if _, err := harness.Reminders.Dispatch(context.Background()); err != nil {
			t.Fatalf("Dispatch 返回错误: %v", err)
		}
		if got := len(harness.Mail.Messages()); got != 0 {
			t.Fatalf("已发送的提醒不应再次发送，实际又发了 %d 封", got)
		}
	})

	t.Run("收件人可在接口查询到自己的提醒", func(t *testing.T) {
		listed := owner.Client.Get(URL("/reminders?page=1&page_size=20")).
			RequireStatus(t, http.StatusOK)
		total := Int(listed.Pagination(t), "total")
		if total == 0 {
			t.Fatal("收件人应能查询到自己的提醒")
		}
		first := listed.ListData(t)[0]
		if Str(first, "status") != domain.ReminderStatusSent {
			t.Errorf("列表里的状态 = %q，期望 sent", Str(first, "status"))
		}
		// 幂等键是实现细节，不应出现在响应里。
		if _, exists := first["dedupe_key"]; exists {
			t.Error("响应不应包含 dedupe_key")
		}
		// 详情与列表同源。
		owner.Client.Get(URL("/reminders/"+Str(first, "id"))).RequireStatus(t, http.StatusOK)
	})

	t.Run("他人看不到我的提醒", func(t *testing.T) {
		outsider := harness.RegisterAndLogin(t, "rem_outsider")
		listed := outsider.Client.Get(URL("/reminders?page=1&page_size=20")).
			RequireStatus(t, http.StatusOK)
		if got := Int(listed.Pagination(t), "total"); got != 0 {
			t.Fatalf("局外人看到的提醒数 = %d，期望 0", got)
		}
	})

	t.Run("已发送的提醒不允许重试", func(t *testing.T) {
		var reminderID string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT id FROM reminders WHERE task_id = $1 AND kind = 'task_overdue'`,
			taskID).Scan(&reminderID); err != nil {
			t.Fatalf("查询提醒 id 失败: %v", err)
		}
		response := owner.Client.Post(URL("/reminders/"+reminderID+"/retry"), nil)
		// 重试即重复发送，而需求要求"同一提醒不重复发送"。
		response.RequireStatus(t, http.StatusConflict)
		response.RequireField(t, "reminder_id")
	})
}

// TestReminderDueSoonFollowsBusinessDays 覆盖"提前 1 个工作日"。
//
// 关键场景：**周一到期**的任务应在上周五提醒。直接减一天会落到周日，
// 那一天没人看邮件，提醒等于没发。
func TestReminderDueSoonFollowsBusinessDays(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "due_owner")
	groupID := harness.CreateGroup(t, owner, "工作日组")
	projectID := harness.CreateProject(t, owner, groupID, "工作日项目")
	// 2026-10-13 是周二，前一个工作日是周一 10-12。
	taskID := harness.CreateTask(t, owner, projectID, "周二到期的任务", "2026-10-01", "2026-10-13")

	version := Str(owner.Client.Get(URL("/tasks/"+taskID)).
		RequireStatus(t, http.StatusOK).Data(t), "updated_at")
	owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"status":              "in_progress",
		"expected_updated_at": version,
	}).RequireStatus(t, http.StatusOK)
	harness.Mail.Reset()

	// 时钟停在周日：此时"提前 1 个工作日"的提醒日尚未到达（应该是周一 10-12 发）。
	harness.Clock.Set(reminderDate(t, "2026-10-11").Add(9*time.Hour + 30*time.Minute))
	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	if got := countReminders(t, harness, taskID, domain.ReminderKindDueSoon); got != 0 {
		t.Fatalf("周日不应生成到期前提醒（提醒日是周一），实际 %d 条", got)
	}

	// 时钟推进到周一 10-12：这一天应生成到期前提醒。
	harness.Clock.Set(reminderDate(t, "2026-10-12").Add(9*time.Hour + 30*time.Minute))
	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	if got := countReminders(t, harness, taskID, domain.ReminderKindDueSoon); got != 1 {
		t.Fatalf("周一时到期前提醒数量 = %d，期望 1", got)
	}

	// 到点了就发得出去：scheduled_for 是本地 09:00，而现在是 09:30。
	report, err := harness.Reminders.Dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch 返回错误: %v", err)
	}
	if report.Sent == 0 {
		t.Fatalf("应发送到期前提醒，报告 = %+v", report)
	}
	if messages := harness.Mail.Messages(); len(messages) == 0 ||
		!strings.Contains(messages[0].Subject, "即将到期") {
		t.Fatalf("应收到'即将到期'主题的邮件，实际 %+v", harness.Mail.Messages())
	}

	// 到了到期日当天不该再发"到期前"（那是延期提醒的语义），
	// 且原有记录不会被改写。
	harness.Clock.Set(reminderDate(t, "2026-10-13").Add(9*time.Hour + 30*time.Minute))
	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	if got := countReminders(t, harness, taskID, domain.ReminderKindDueSoon); got != 1 {
		t.Fatalf("到期前提醒数量 = %d，期望仍为 1", got)
	}
}

// TestReminderSkipsIneligibleRecipients 覆盖收件人资格（需求文档 5.7）。
func TestReminderSkipsIneligibleRecipients(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "skip_owner")
	groupID := harness.CreateGroup(t, owner, "资格组")
	projectID := harness.CreateProject(t, owner, groupID, "资格项目")
	taskID := harness.CreateTask(t, owner, projectID, "资格任务", "2026-09-28", "2026-10-05")

	t.Run("关闭提醒的用户不生成提醒", func(t *testing.T) {
		// 通过资料接口关掉自己的邮件提醒。
		setReminderEnabled(t, owner, false)

		report, err := harness.Reminders.Generate(context.Background())
		if err != nil {
			t.Fatalf("Generate 返回错误: %v", err)
		}
		if report.SkippedRecipients == 0 {
			t.Fatal("应统计到被跳过的收件人")
		}
		if got := countReminders(t, harness, taskID, domain.ReminderKindOverdue); got != 0 {
			t.Fatalf("关闭提醒的用户不该产生提醒行，实际 %d 条", got)
		}
	})

	t.Run("重新开启后生成", func(t *testing.T) {
		setReminderEnabled(t, owner, true)
		if _, err := harness.Reminders.Generate(context.Background()); err != nil {
			t.Fatalf("Generate 返回错误: %v", err)
		}
		if got := countReminders(t, harness, taskID, domain.ReminderKindOverdue); got != 1 {
			t.Fatalf("重新开启后应有 1 条提醒，实际 %d 条", got)
		}
	})

	t.Run("派发前收件人被停用则记为 skipped", func(t *testing.T) {
		// 生成与发送之间收件人可能失效，因此派发时要**重新判定**。
		if _, err := harness.Pool.Exec(context.Background(),
			`UPDATE users SET reminder_enabled = false WHERE id = $1`, owner.UserID); err != nil {
			t.Fatalf("关闭提醒失败: %v", err)
		}

		harness.Mail.Reset()
		report, err := harness.Reminders.Dispatch(context.Background())
		if err != nil {
			t.Fatalf("Dispatch 返回错误: %v", err)
		}
		if report.Skipped == 0 {
			t.Fatalf("应跳过而不发送，报告 = %+v", report)
		}
		if got := len(harness.Mail.Messages()); got != 0 {
			t.Fatalf("不该发送邮件，实际发了 %d 封", got)
		}
	})
}

// TestReminderFailureRecordsReasonAndCanRetry 覆盖失败记录与手动重试。
func TestReminderFailureRecordsReasonAndCanRetry(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "fail_owner")
	groupID := harness.CreateGroup(t, owner, "失败组")
	projectID := harness.CreateProject(t, owner, groupID, "失败项目")
	taskID := harness.CreateTask(t, owner, projectID, "失败任务", "2026-09-28", "2026-10-05")

	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	// 注入发送失败。
	harness.Mail.FailWith(errors.New("SMTP 连接超时"))
	report, err := harness.Reminders.Dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch 返回错误: %v", err)
	}
	if report.Failed == 0 {
		t.Fatalf("应记录失败，报告 = %+v", report)
	}

	var reminderID, status, lastError string
	var attempts int
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT id, status, last_error, attempts FROM reminders
		  WHERE task_id = $1 AND kind = 'task_overdue'`,
		taskID).Scan(&reminderID, &status, &lastError, &attempts); err != nil {
		t.Fatalf("查询提醒失败: %v", err)
	}
	if status != domain.ReminderStatusFailed {
		t.Fatalf("状态 = %q，期望 failed", status)
	}
	// 需求文档 5.7：失败原因与重试次数都要记录，且可查询。
	if !strings.Contains(lastError, "SMTP 连接超时") {
		t.Fatalf("失败原因未记录，实际 %q", lastError)
	}
	if attempts != 1 {
		t.Fatalf("重试次数 = %d，期望 1", attempts)
	}
	owner.Client.Get(URL("/reminders/"+reminderID)).
		RequireStatus(t, http.StatusOK)

	t.Run("手动重试后重新发送成功", func(t *testing.T) {
		harness.Mail.FailWith(nil)
		owner.Client.Post(URL("/reminders/"+reminderID+"/retry"), nil).
			RequireStatus(t, http.StatusOK)

		report, err := harness.Reminders.Dispatch(context.Background())
		if err != nil {
			t.Fatalf("Dispatch 返回错误: %v", err)
		}
		if report.Sent == 0 {
			t.Fatalf("重试后应发送成功，报告 = %+v", report)
		}
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT status, attempts FROM reminders WHERE id = $1`,
			reminderID).Scan(&status, &attempts); err != nil {
			t.Fatalf("查询提醒失败: %v", err)
		}
		if status != domain.ReminderStatusSent {
			t.Fatalf("重试后状态 = %q，期望 sent", status)
		}
		if attempts != 2 {
			t.Fatalf("重试次数 = %d，期望 2（失败 1 次 + 成功 1 次）", attempts)
		}
	})
}

// TestReminderEventKinds 覆盖事件型提醒：新任务分配与持有者变更。
//
// 这两类**不是**时间驱动的，因此由 tasks 在事务提交后插入一行，
// 发送仍走同一个派发循环。
func TestReminderEventKinds(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "ev_owner")
	member := harness.RegisterAndLogin(t, "ev_member")
	groupID := harness.CreateGroup(t, owner, "事件组")
	projectID := harness.CreateProject(t, owner, groupID, "事件项目")
	taskID := harness.CreateTask(t, owner, projectID, "事件任务", "2026-10-01", "2026-10-30")

	harness.AddProjectMember(t, owner, projectID, member.UserID)
	// 加入任务成员即"新任务分配"。
	owner.Client.Post(URL("/tasks/"+taskID+"/members"),
		map[string]any{"user_id": member.UserID}).RequireStatus(t, http.StatusCreated)

	if got := countRemindersForUser(t, harness, taskID, member.UserID,
		domain.ReminderKindAssigned); got != 1 {
		t.Fatalf("新任务分配提醒数量 = %d，期望 1", got)
	}

	// 转移持有者即"持有者变更"。
	owner.Client.Patch(URL("/tasks/"+taskID+"/members/"+member.UserID),
		map[string]any{"role": "owner"}).RequireStatus(t, http.StatusOK)

	if got := countRemindersForUser(t, harness, taskID, member.UserID,
		domain.ReminderKindOwnerChanged); got != 1 {
		t.Fatalf("持有者变更提醒数量 = %d，期望 1", got)
	}

	t.Run("事件型提醒由派发循环发送", func(t *testing.T) {
		harness.Mail.Reset()
		report, err := harness.Reminders.Dispatch(context.Background())
		if err != nil {
			t.Fatalf("Dispatch 返回错误: %v", err)
		}
		if report.Sent == 0 {
			t.Fatalf("应发送事件型提醒，报告 = %+v", report)
		}
	})
}

// TestReminderListScoping 覆盖列表的作用域与参数校验。
func TestReminderListScoping(t *testing.T) {
	harness := NewHarness(t)

	admin := harness.RegisterAndLogin(t, "rl_admin")
	promoteToAdmin(t, harness, admin.UserID)
	owner := harness.RegisterAndLogin(t, "rl_owner")
	groupID := harness.CreateGroup(t, owner, "作用域组")
	projectID := harness.CreateProject(t, owner, groupID, "作用域项目")
	taskID := harness.CreateTask(t, owner, projectID, "作用域任务", "2026-09-28", "2026-10-05")

	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	t.Run("管理员可以看到全部提醒", func(t *testing.T) {
		listed := admin.Client.Get(URL("/reminders?page=1&page_size=50")).
			RequireStatus(t, http.StatusOK)
		if got := Int(listed.Pagination(t), "total"); got == 0 {
			t.Fatal("管理员应能看到全部提醒（用于排查谁没收到、为什么没收到）")
		}
	})

	t.Run("按类型筛选", func(t *testing.T) {
		listed := owner.Client.Get(URL("/reminders?kind=task_overdue&page=1&page_size=50")).
			RequireStatus(t, http.StatusOK)
		for _, item := range listed.ListData(t) {
			if Str(item, "kind") != "task_overdue" {
				t.Fatalf("筛选失效，出现了 %q", Str(item, "kind"))
			}
		}
	})

	t.Run("未知查询参数返回 400", func(t *testing.T) {
		// 把 kind 拼成 kinds 会被静默忽略，客户端以为筛选生效了。
		owner.Client.Get(URL("/reminders?kinds=task_overdue")).
			RequireStatus(t, http.StatusBadRequest)
	})

	t.Run("排序字段非法返回 400", func(t *testing.T) {
		owner.Client.Get(URL("/reminders?sort=dedupe_key")).
			RequireStatus(t, http.StatusBadRequest)
	})

	t.Run("不存在的提醒返回 404", func(t *testing.T) {
		owner.Client.Get(URL("/reminders/01a1290b-0ae8-7046-9106-30f2d07815af")).
			RequireStatus(t, http.StatusNotFound)
	})

	t.Run("他人的提醒详情返回 404", func(t *testing.T) {
		other := harness.RegisterAndLogin(t, "rl_other")
		var reminderID string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT id FROM reminders WHERE task_id = $1 LIMIT 1`, taskID).Scan(&reminderID); err != nil {
			t.Fatalf("查询提醒失败: %v", err)
		}
		other.Client.Get(URL("/reminders/"+reminderID)).RequireStatus(t, http.StatusNotFound)
	})
}

// setReminderEnabled 通过资料接口开关自己的邮件提醒。
//
// 资料更新需要版本字段（契约 2.6），因此先取当前 updated_at。
func setReminderEnabled(t *testing.T, actor Authenticated, enabled bool) {
	t.Helper()

	version := Str(actor.Client.Get(URL("/users/"+actor.UserID)).
		RequireStatus(t, http.StatusOK).Data(t), "updated_at")
	actor.Client.Patch(URL("/users/"+actor.UserID), map[string]any{
		"reminder_enabled":    enabled,
		"expected_updated_at": version,
	}).RequireStatus(t, http.StatusOK)
}

// TestReminderMailDisabledIsSkippedNotFailed 锁住"邮件未启用 ≠ 发送失败"。
//
// 这条断言是冒烟测试（真实二进制 + 真实数据库）发现的：
// 实现最初把 mail.ErrDisabled 也记成 failed，于是失败列表里全是
// "邮件没配"，而 failed 的语义是"需要人排查"。
// 领域层对 skipped 的定义里本来就包含"邮件未启用"这一项。
func TestReminderMailDisabledIsSkippedNotFailed(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "off_owner")
	groupID := harness.CreateGroup(t, owner, "未启用组")
	projectID := harness.CreateProject(t, owner, groupID, "未启用项目")
	taskID := harness.CreateTask(t, owner, projectID, "未启用任务", "2026-09-28", "2026-10-05")

	if _, err := harness.Reminders.Generate(context.Background()); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	// 用一个"未启用"的发送器替换掉假发送器。
	disabled := reminders.NewService(reminders.Options{
		Store:  reminders.NewRepository(harness.Pool),
		Sender: mail.Disabled{},
		IDs:    harness.IDs,
		Clock:  harness.Clock,
		Logger: harness.Logger,
	})
	report, err := disabled.Dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch 返回错误: %v", err)
	}
	if report.Failed != 0 {
		t.Fatalf("邮件未启用不该记成失败，报告 = %+v", report)
	}
	if report.Skipped == 0 {
		t.Fatalf("邮件未启用应记成 skipped，报告 = %+v", report)
	}

	var status, lastError string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT status, last_error FROM reminders
		  WHERE task_id = $1 AND kind = 'task_overdue'`,
		taskID).Scan(&status, &lastError); err != nil {
		t.Fatalf("查询提醒失败: %v", err)
	}
	if status != domain.ReminderStatusSkipped {
		t.Fatalf("状态 = %q，期望 skipped", status)
	}
	if !strings.Contains(lastError, "邮件功能未启用") {
		t.Fatalf("跳过原因应说明邮件未启用，实际 %q", lastError)
	}
}

// countReminders 统计某任务某类型的提醒数量。'
func countReminders(t *testing.T, harness *Harness, taskID, kind string) int {
	t.Helper()

	var count int
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reminders WHERE task_id = $1 AND kind = $2`,
		taskID, kind).Scan(&count); err != nil {
		t.Fatalf("统计提醒失败: %v", err)
	}
	return count
}

// countRemindersForUser 统计某任务某类型发给某用户的提醒数量。
func countRemindersForUser(t *testing.T, harness *Harness, taskID, userID, kind string) int {
	t.Helper()

	var count int
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reminders WHERE task_id = $1 AND recipient_id = $2 AND kind = $3`,
		taskID, userID, kind).Scan(&count); err != nil {
		t.Fatalf("统计提醒失败: %v", err)
	}
	return count
}
