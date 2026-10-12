package testsupport

import (
	"testing"
)

// 任务"最后修改人"（tasks.updated_by，迁移 000002）的不变式测试。
//
// 需求文档 5.6 要求组内汇总页能"查看任务最后修改人和最后修改时间"。
// 时间由 updated_at 提供，人由 updated_by 提供；两者必须描述**同一个事件**。
//
// 这条不变式值得单独测，是因为它的失效方式非常安静：漏传一个参数不会报错，
// 字段只是停在旧值上——而"最后修改人"显示的是别人，恰恰是排查问题时最误导的信息。

// taskUpdatedBy 读取任务当前的 updated_by 与 updated_at。
func taskUpdatedBy(t *testing.T, _ *Harness, actor Authenticated, taskID string) (string, string) {
	t.Helper()

	data := actor.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200).Data(t)
	return Str(data, "updated_by"), Str(data, "updated_at")
}

// TestTaskUpdatedByAdvancesWithUpdatedAt 覆盖全部写路径。
func TestTaskUpdatedByAdvancesWithUpdatedAt(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "ub_alice")
	bob := harness.RegisterAndLogin(t, "ub_bob")
	carol := harness.RegisterAndLogin(t, "ub_carol")

	groupID := harness.CreateGroup(t, alice, "最后修改人工作组")
	projectID := harness.CreateProject(t, alice, groupID, "最后修改人项目")
	// bob 与 carol 都是项目成员，因此都有写权限。
	for _, member := range []Authenticated{bob, carol} {
		alice.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)
	}

	taskID := harness.CreateTask(t, alice, projectID, "被反复修改的任务", "2026-10-01", "2026-10-05")

	// 创建：创建者同时也是最后修改人。
	updatedBy, updatedAt := taskUpdatedBy(t, harness, alice, taskID)
	if updatedBy != alice.UserID {
		t.Fatalf("创建后的 updated_by = %q，期望创建者 %q", updatedBy, alice.UserID)
	}

	// 普通 PATCH：由 bob 执行，最后修改人必须变成 bob，且时间前进。
	bob.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"name":                "bob 改的名字",
		"expected_updated_at": updatedAt,
	}).RequireStatus(t, 200)

	updatedBy, previous := taskUpdatedBy(t, harness, bob, taskID)
	if updatedBy != bob.UserID {
		t.Errorf("PATCH 后的 updated_by = %q，期望 %q", updatedBy, bob.UserID)
	}
	if previous == updatedAt {
		t.Error("PATCH 必须同时推进 updated_at")
	}

	// 甘特图拖拽改日期（专用端点）也是一次修改。
	bob.Client.Patch(URL("/tasks/"+taskID+"/dates"), map[string]any{
		"start_date":          "2026-10-02",
		"end_date":            "2026-10-06",
		"expected_updated_at": previous,
	}).RequireStatus(t, 200)

	updatedBy, previous = taskUpdatedBy(t, harness, bob, taskID)
	if updatedBy != bob.UserID {
		t.Errorf("改日期后的 updated_by = %q，期望 %q", updatedBy, bob.UserID)
	}

	// 成员变更**不触碰 tasks 行**，因此两个人都不应该变。
	// 这是刻意的语义：updated_by 跟踪的是"任务本身的修改"，
	// 成员关系的变更由 Audit（activity_logs）阶段记录。
	bob.Client.Post(URL("/tasks/"+taskID+"/members"), map[string]any{
		"user_id": carol.UserID,
	}).RequireStatus(t, 201)

	afterMemberChange, afterMemberAt := taskUpdatedBy(t, harness, bob, taskID)
	if afterMemberChange != bob.UserID {
		t.Errorf("成员变更后 updated_by = %q，期望保持不变（%q）", afterMemberChange, bob.UserID)
	}
	if afterMemberAt != previous {
		t.Errorf("成员变更不应推进 updated_at：%q → %q", previous, afterMemberAt)
	}

	// 逻辑删除：谁删的同样必须能回答（需求文档 5.8 把删除列为关键操作）。
	alice.Client.Delete(URL("/tasks/"+taskID), map[string]any{
		"expected_updated_at": afterMemberAt,
	}).RequireStatus(t, 204)

	var deletedBy string
	if err := harness.Pool.QueryRow(t.Context(),
		`SELECT updated_by FROM tasks WHERE id = $1`, taskID).Scan(&deletedBy); err != nil {
		t.Fatalf("查询已删除任务的 updated_by 失败: %v", err)
	}
	if deletedBy != alice.UserID {
		t.Errorf("逻辑删除后的 updated_by = %q，期望执行者 %q", deletedBy, alice.UserID)
	}
}

// TestTaskCopySetsNewTaskUpdatedByToCopier 覆盖复制路径。
//
// 复制的语义是"新任务"，因此它的最后修改人应当是执行复制的人，
// 而不是源任务的最后修改人——否则新任务一诞生就带着别人的名字。
func TestTaskCopySetsNewTaskUpdatedByToCopier(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "ub_copy_alice")
	bob := harness.RegisterAndLogin(t, "ub_copy_bob")

	groupID := harness.CreateGroup(t, alice, "复制最后修改人工作组")
	projectID := harness.CreateProject(t, alice, groupID, "复制最后修改人项目")
	alice.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": bob.UserID}).RequireStatus(t, 201)

	taskID := harness.CreateTask(t, alice, projectID, "源任务", "2026-10-01", "2026-10-05")

	// alice 先改一次，让源任务的最后修改人是 alice（与创建者相同，这里换个执行者）。
	_, version := taskUpdatedBy(t, harness, alice, taskID)
	bob.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"name":                "bob 改过的源任务",
		"expected_updated_at": version,
	}).RequireStatus(t, 200)

	copied := alice.Client.PostWithHeaders(URL("/tasks/"+taskID+"/copy"), map[string]any{},
		map[string]string{"Idempotency-Key": "ub-copy-key"})
	copied.RequireStatus(t, 201)
	copyID := Str(copied.Data(t), "id")

	updatedBy, _ := taskUpdatedBy(t, harness, alice, copyID)
	if updatedBy != alice.UserID {
		t.Errorf("副本的 updated_by = %q，期望复制者 %q（而不是源任务的最后修改人）",
			updatedBy, alice.UserID)
	}
}
