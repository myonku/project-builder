package domain

import (
	"testing"
)

// TestPreviousBusinessDay 覆盖"提前 1 个工作日"的核心推算。
//
// 2026-10-11 是周日，因此这一周的构成为：
// 周一 10-05、周二 10-06、周三 10-07、周四 10-08、周五 10-09、
// 周六 10-10、周日 10-11、周一 10-12。
func TestPreviousBusinessDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		date string
		want string
	}{
		{name: "周二的前一个工作日是周一", date: "2026-10-06", want: "2026-10-05"},
		{name: "周一的前一个工作日是上周五", date: "2026-10-12", want: "2026-10-09"},
		{name: "周六的前一个工作日是周五", date: "2026-10-10", want: "2026-10-09"},
		{name: "周日的前一个工作日是周五", date: "2026-10-11", want: "2026-10-09"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PreviousBusinessDay(mustDate(t, tc.date))
			if got.Format(DateLayout) != tc.want {
				t.Fatalf("PreviousBusinessDay(%s) = %s，期望 %s",
					tc.date, got.Format(DateLayout), tc.want)
			}
		})
	}
}

// TestIsReminderDayFor 覆盖"哪一天该发到期前提醒"。
func TestIsReminderDayFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		today string
		date  string
		want  bool
	}{
		{name: "周一到期时上周五提醒", today: "2026-10-09", date: "2026-10-12", want: true},
		{name: "到期前一天不是工作日则不提醒", today: "2026-10-11", date: "2026-10-12"},
		{name: "到期当天不提醒（那是逾期）", today: "2026-10-12", date: "2026-10-12"},
		{name: "工作日提前一天提醒", today: "2026-10-05", date: "2026-10-06", want: true},
		{name: "提前两天不提醒", today: "2026-10-04", date: "2026-10-06"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsReminderDayFor(mustDate(t, tc.today), mustDate(t, tc.date))
			if got != tc.want {
				t.Fatalf("IsReminderDayFor(%s, %s) = %v，期望 %v",
					tc.today, tc.date, got, tc.want)
			}
		})
	}
}

// TestReminderDedupeKeyIncludesWindow 锁住幂等键必须含时间窗口。
//
// 这是"改了到期日之后再也收不到提醒"这类缺陷的唯一防线：
// 键里没有窗口，同一条提醒一辈子只能存在一行。
func TestReminderDedupeKeyIncludesWindow(t *testing.T) {
	t.Parallel()

	first := ReminderDedupeKey(ReminderKindDueSoon, "task-1", "user-1", "2026-10-12")
	second := ReminderDedupeKey(ReminderKindDueSoon, "task-1", "user-1", "2026-11-02")
	if first == second {
		t.Fatal("到期日不同时必须得到不同的幂等键")
	}

	// 其它三个维度各自都能区分开。
	if ReminderDedupeKey(ReminderKindDueSoon, "task-1", "user-1", "d") ==
		ReminderDedupeKey(ReminderKindOverdue, "task-1", "user-1", "d") {
		t.Error("类型不同的键不应相同")
	}
	if ReminderDedupeKey(ReminderKindDueSoon, "task-1", "user-1", "d") ==
		ReminderDedupeKey(ReminderKindDueSoon, "task-2", "user-1", "d") {
		t.Error("任务不同的键不应相同")
	}
	if ReminderDedupeKey(ReminderKindDueSoon, "task-1", "user-1", "d") ==
		ReminderDedupeKey(ReminderKindDueSoon, "task-1", "user-2", "d") {
		t.Error("收件人不同的键不应相同")
	}
}

// TestCanReceiveReminder 覆盖收件人资格（需求文档 5.7）。
func TestCanReceiveReminder(t *testing.T) {
	t.Parallel()

	active := ReminderRecipient{
		UserID: "u1", Email: "a@example.com",
		Status: UserStatusActive, ReminderEnabled: true,
	}
	withoutEmail := active
	withoutEmail.Email = "  "
	disabled := active
	disabled.Status = UserStatusDisabled
	optedOut := active
	optedOut.ReminderEnabled = false

	tests := []struct {
		name      string
		recipient ReminderRecipient
		want      bool
	}{
		{name: "正常用户可收", recipient: active, want: true},
		{name: "无邮箱不收", recipient: withoutEmail},
		{name: "已停用不收", recipient: disabled},
		{name: "关闭提醒不收", recipient: optedOut},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanReceiveReminder(tc.recipient); got != tc.want {
				t.Fatalf("CanReceiveReminder = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestReminderTaskStillApplies 覆盖"不再适用时不发送"。
func TestReminderTaskStillApplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		task ReminderTask
		want bool
	}{
		{name: "进行中仍适用", task: ReminderTask{Status: TaskStatusInProgress}, want: true},
		{name: "未开始仍适用", task: ReminderTask{Status: TaskStatusNotStarted}, want: true},
		{name: "已完成不适用", task: ReminderTask{Status: TaskStatusCompleted}},
		{name: "已取消不适用", task: ReminderTask{Status: TaskStatusCancelled}},
		{name: "已删除不适用", task: ReminderTask{Status: TaskStatusInProgress, Deleted: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ReminderTaskStillApplies(tc.task); got != tc.want {
				t.Fatalf("ReminderTaskStillApplies = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestReminderKindAppliesToStatus 覆盖"未开始的任务不收到期前提醒"。
func TestReminderKindAppliesToStatus(t *testing.T) {
	t.Parallel()

	if ReminderKindAppliesToStatus(ReminderKindDueSoon, TaskStatusNotStarted) {
		t.Error("尚未开始的任务不该收到期前提醒（该收的是开始前提醒）")
	}
	if !ReminderKindAppliesToStatus(ReminderKindStartSoon, TaskStatusNotStarted) {
		t.Error("尚未开始的任务应能收开始前提醒")
	}
	if !ReminderKindAppliesToStatus(ReminderKindDueSoon, TaskStatusInProgress) {
		t.Error("进行中的任务应能收到期前提醒")
	}
	if ReminderKindAppliesToStatus(ReminderKindDueSoon, TaskStatusCompleted) {
		t.Error("已完成的任务不该收到期前提醒")
	}
}

// TestReminderTaskURL 覆盖链接模板渲染。
func TestReminderTaskURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		template  string
		projectID string
		taskID    string
		want      string
	}{
		{name: "正常替换", template: "https://x/tasks/{task_id}", taskID: "t1", want: "https://x/tasks/t1"},
		{name: "模板为空则无链接", template: "  ", taskID: "t1", want: ""},
		{name: "无占位符时追加", template: "https://x/tasks/", taskID: "t1", want: "https://x/tasks/t1"},
		{name: "项目详情任务定位", template: "https://x/projects/{project_id}?taskId={task_id}", projectID: "p1", taskID: "t1", want: "https://x/projects/p1?taskId=t1"},
		{name: "部署子路径与重复占位符", template: "https://x/worklane/projects/{project_id}?taskId={task_id}&project={project_id}", projectID: "p1", taskID: "t1", want: "https://x/worklane/projects/p1?taskId=t1&project=p1"},
		{name: "缺少项目不暴露未展开链接", template: "https://x/projects/{project_id}?taskId={task_id}", taskID: "t1", want: ""},
		{name: "缺少任务则无链接", template: "https://x/projects/{project_id}?taskId={task_id}", projectID: "p1", want: ""},
		{name: "仅项目占位符保留追加兼容", template: "https://x/projects/{project_id}/tasks/", projectID: "p1", taskID: "t1", want: "https://x/projects/p1/tasks/t1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ReminderTaskURL(tc.template, tc.projectID, tc.taskID); got != tc.want {
				t.Fatalf("ReminderTaskURL = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestReminderVocabulary 锁住枚举集合与标签的完整性。
func TestReminderVocabulary(t *testing.T) {
	t.Parallel()

	for _, kind := range ReminderKinds {
		if ReminderKindLabel(kind) == kind {
			t.Errorf("类型 %q 缺少中文标签", kind)
		}
	}
	if len(ReminderStatuses) != 4 {
		t.Fatalf("状态数量 = %d，期望 4", len(ReminderStatuses))
	}
	// 跳过与失败必须是两个不同状态：混在一起会让"失败列表"永远噪声不断。
	if ReminderStatusSkipped == ReminderStatusFailed {
		t.Fatal("skipped 与 failed 必须是不同状态")
	}
}
