package testsupport

import "testing"

// TestAuditCoversTaskWrites 覆盖任务域的八类关键写操作。
//
// 与 TestAuditCoversProjectWrites 同一目的：证明"做了什么 ⇒ 记录里有什么"，
// 且记录与业务写在**同一个事务**里（被拒绝的写入不留记录）。
// 助手 auditRowsFor / requireAuditRow / countAuditRowsFor 定义在 audit_coverage_test.go。
func TestAuditCoversTaskWrites(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "audit_task_owner")
	first := harness.RegisterAndLogin(t, "audit_task_first")
	second := harness.RegisterAndLogin(t, "audit_task_second")
	// outsider 是有效用户但不是项目成员：用于验证"任务成员必须是项目成员"被拒时不留记录。
	outsider := harness.RegisterAndLogin(t, "audit_task_outsider")

	groupID := harness.CreateGroup(t, owner, "任务审计覆盖组")
	projectID := harness.CreateProject(t, owner, groupID, "任务审计覆盖项目")
	harness.AddProjectMember(t, owner, projectID, first.UserID)
	harness.AddProjectMember(t, owner, projectID, second.UserID)

	taskID := harness.CreateTask(t, owner, projectID, "审计覆盖任务", "2026-10-01", "2026-10-31")

	// 创建：object_id 是新任务 id，changes 至少含 name 与 project_id。
	rows := auditRowsFor(t, harness, taskID)
	created := requireAuditRow(t, rows, "created", "审计覆盖任务", projectID)
	if created.ActorID != owner.UserID {
		t.Fatalf("created 记录的 actor_id = %s，期望 %s", created.ActorID, owner.UserID)
	}
	if created.RequestID == "" {
		t.Fatal("created 记录缺少 request_id（说明请求链路没有把 request id 传到用例层）")
	}

	// PATCH /tasks/{id}：name → updated（只记实际提供的字段）。
	current := owner.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200)
	staleVersion := Str(current.Data(t), "updated_at")

	owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"name":                "审计覆盖任务（改名）",
		"expected_updated_at": staleVersion,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, taskID)
	requireAuditRow(t, rows, "updated", "审计覆盖任务（改名）")

	// PATCH /tasks/{id}/dates：start_date + end_date → updated。
	current = owner.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200)
	owner.Client.Patch(URL("/tasks/"+taskID+"/dates"), map[string]any{
		"start_date":          "2026-10-05",
		"end_date":            "2026-10-20",
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, taskID)
	requireAuditRow(t, rows, "updated", "2026-10-05", "2026-10-20")

	// 复制：记在**新任务**上（object_id = 新 id），而不是源任务。
	copied := owner.Client.Post(URL("/tasks/"+taskID+"/copy"), map[string]any{
		"name": "审计覆盖任务（复制）",
	}).RequireStatus(t, 201)
	copiedID := Str(copied.Data(t), "id")
	if copiedID == "" || copiedID == taskID {
		t.Fatalf("复制返回的任务 id = %q，期望一个新 id（源 id = %s）", copiedID, taskID)
	}

	copiedRows := auditRowsFor(t, harness, copiedID)
	requireAuditRow(t, copiedRows, "created", "审计覆盖任务（复制）", projectID)

	// 成员：添加 → member_added（object_id 是任务这个容器）。
	owner.Client.Post(URL("/tasks/"+taskID+"/members"), map[string]any{
		"user_id": first.UserID,
	}).RequireStatus(t, 201)
	owner.Client.Post(URL("/tasks/"+taskID+"/members"), map[string]any{
		"user_id": second.UserID,
	}).RequireStatus(t, 201)

	rows = auditRowsFor(t, harness, taskID)
	requireAuditRow(t, rows, "member_added", first.UserID, "member")
	requireAuditRow(t, rows, "member_added", second.UserID, "member")

	// 持有者转移 → owner_transferred（记录在新任持有者身上）。
	owner.Client.Patch(URL("/tasks/"+taskID+"/members/"+first.UserID),
		map[string]any{"role": "owner"}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, taskID)
	requireAuditRow(t, rows, "owner_transferred", first.UserID)

	// 转移后 owner 已降为任务 member，移除成员应仍可由项目持有者发起。
	owner.Client.Delete(URL("/tasks/"+taskID+"/members/"+second.UserID), nil).
		RequireStatus(t, 204)

	rows = auditRowsFor(t, harness, taskID)
	requireAuditRow(t, rows, "member_removed", second.UserID)

	// 被拒绝的写入不得留下记录（一）：版本冲突。
	before := countAuditRowsFor(t, harness, taskID)
	owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"name":                "不应生效",
		"expected_updated_at": staleVersion,
	}).RequireStatus(t, 409)
	if after := countAuditRowsFor(t, harness, taskID); after != before {
		t.Fatalf("版本冲突的写入留下了 %d 条新记录（期望 0）", after-before)
	}

	// 被拒绝的写入不得留下记录（二）：目标用户不是项目成员（422）。
	before = countAuditRowsFor(t, harness, taskID)
	owner.Client.Post(URL("/tasks/"+taskID+"/members"), map[string]any{
		"user_id": outsider.UserID,
	}).RequireStatus(t, 422).RequireCode(t, "validation_error")
	if after := countAuditRowsFor(t, harness, taskID); after != before {
		t.Fatalf("非项目成员加入任务被拒后留下了 %d 条新记录（期望 0）", after-before)
	}

	// 被拒绝的写入不得留下记录（三）：任务持有者唯一，降级唯一持有者返回 403。
	//
	// 这同时说明 member_role_changed 在本模块**无法由 HTTP 触发**：
	// 任务至多一个持有者（部分唯一索引 task_single_owner），而"降级"要求
	// ownerCount > 1，因此该分支只在用例层保留、不会被真实请求走到。
	before = countAuditRowsFor(t, harness, taskID)
	owner.Client.Patch(URL("/tasks/"+taskID+"/members/"+first.UserID),
		map[string]any{"role": "member"}).RequireStatus(t, 403)
	if after := countAuditRowsFor(t, harness, taskID); after != before {
		t.Fatalf("降级唯一持有者被拒后留下了 %d 条新记录（期望 0）", after-before)
	}

	// 逻辑删除 → deleted。
	current = owner.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200)
	owner.Client.Delete(URL("/tasks/"+taskID), map[string]any{
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	}).RequireStatus(t, 204)

	rows = auditRowsFor(t, harness, taskID)
	requireAuditRow(t, rows, "deleted")
}
