package testsupport

import (
	"net/http"
	"strings"
	"testing"
)

// TestAuditCoversGroupWrites 覆盖工作组域的全部关键写路径（backend/docs/B05 §5.17 的覆盖矩阵）。
//
// 与项目域的同名用例同理：审计接入点分散在多个写路径上，漏掉一处不会报错，
// 只会让某个操作没有历史。因此每条路径都要有一条"做了什么 ⇒ 记录里有什么"的断言。
//
// 成员变更记在**容器**（工作组）上，不是成员行 id（《A09》）。
func TestAuditCoversGroupWrites(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "audit_group_owner")
	first := harness.RegisterAndLogin(t, "audit_group_first")
	second := harness.RegisterAndLogin(t, "audit_group_second")
	applicant := harness.RegisterAndLogin(t, "audit_group_applicant")

	// 创建工作组 → created（changes 至少含 name）。
	groupID := harness.CreateGroup(t, owner, "审计覆盖组")

	rows := auditRowsFor(t, harness, groupID)
	created := requireAuditRow(t, rows, "created", "审计覆盖组")
	if created.ActorID != owner.UserID {
		t.Fatalf("created 记录的 actor_id = %s，期望 %s", created.ActorID, owner.UserID)
	}
	if created.RequestID == "" {
		t.Fatal("created 记录缺少 request_id（说明请求链路没有把 request id 传到用例层）")
	}

	// 修改工作组 → updated（只记本次实际提供的字段）。
	current := owner.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, 200)
	updatedAt := Str(current.Data(t), "updated_at")

	owner.Client.Patch(URL("/groups/"+groupID), map[string]any{
		"name":                "审计覆盖组（改名）",
		"expected_updated_at": updatedAt,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, groupID)
	updated := requireAuditRow(t, rows, "updated", "审计覆盖组（改名）")
	if strings.Contains(updated.Changes, "status") {
		t.Fatalf("未提供的字段进了 changes：%s", updated.Changes)
	}

	// 添加成员 → member_added（user_id + role）。
	harness.AddGroupMember(t, owner, groupID, first.UserID)
	rows = auditRowsFor(t, harness, groupID)
	requireAuditRow(t, rows, "member_added", first.UserID, "member")

	// 修改成员角色 → member_role_changed。
	owner.Client.Patch(URL("/groups/"+groupID+"/members/"+first.UserID),
		map[string]any{"role": "manager"}).RequireStatus(t, 200)
	rows = auditRowsFor(t, harness, groupID)
	requireAuditRow(t, rows, "member_role_changed", first.UserID, "manager")

	// 移除成员 → member_removed（软移除，记在容器上）。
	harness.AddGroupMember(t, owner, groupID, second.UserID)
	owner.Client.Delete(URL("/groups/"+groupID+"/members/"+second.UserID), nil).
		RequireStatus(t, 204)
	rows = auditRowsFor(t, harness, groupID)
	requireAuditRow(t, rows, "member_removed", second.UserID)

	// 提交加入申请 → created（object_id 是工作组，changes 带申请人）。
	applicant.Client.Post(URL("/groups/"+groupID+"/join-requests"), map[string]any{}).
		RequireStatus(t, 201)
	rows = auditRowsFor(t, harness, groupID)
	requireAuditRow(t, rows, "created", applicant.UserID)

	// 审核加入申请 → join_request_reviewed（带审核结果）。
	owner.Client.Post(URL("/groups/"+groupID+"/join-requests/"+applicant.UserID+"/review"),
		map[string]any{"status": "approved"}).RequireStatus(t, 200)
	rows = auditRowsFor(t, harness, groupID)
	requireAuditRow(t, rows, "join_request_reviewed", applicant.UserID, "approved")

	// 所有权转移 → owner_transferred（changes 同时带新任与前任）。
	current = owner.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, 200)
	transferVersion := Str(current.Data(t), "updated_at")

	owner.Client.Patch(URL("/groups/"+groupID), map[string]any{
		"owner_id":            first.UserID,
		"expected_updated_at": transferVersion,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, groupID)
	requireAuditRow(t, rows, "owner_transferred", first.UserID, owner.UserID)

	// 失败的写入不得留下记录：移除唯一所有者（403）。
	before := countAuditRowsFor(t, harness, groupID)
	first.Client.Delete(URL("/groups/"+groupID+"/members/"+first.UserID), nil).
		RequireStatus(t, http.StatusForbidden).
		RequireCode(t, "permission_denied")
	if after := countAuditRowsFor(t, harness, groupID); after != before {
		t.Fatalf("被拒的成员移除留下了 %d 条新记录（期望 0）", after-before)
	}

	// 失败的写入不得留下记录：版本冲突（409）。
	before = countAuditRowsFor(t, harness, groupID)
	first.Client.Patch(URL("/groups/"+groupID), map[string]any{
		"name":                "不应生效",
		"expected_updated_at": transferVersion,
	}).RequireStatus(t, http.StatusConflict).
		RequireCode(t, "version_conflict")
	if after := countAuditRowsFor(t, harness, groupID); after != before {
		t.Fatalf("版本冲突的写入留下了 %d 条新记录（期望 0）", after-before)
	}
}
