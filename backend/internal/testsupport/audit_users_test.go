package testsupport

import (
	"strings"
	"testing"
)

// TestAuditCoversUserWrites 覆盖 Auth 与 Users 两个域的关键写操作。
//
// 两个域写同一张 users 表，但入口不同：
//   - 自助注册（auth）与管理员代建（users）共用同一套实现，**操作人不同**；
//   - 资料更新 / 停用 / 重置密码都要求操作人身份，记录里必须能看出是谁做的。
func TestAuditCoversUserWrites(t *testing.T) {
	harness := NewHarness(t)

	member := harness.RegisterAndLogin(t, "audit_user_member")
	admin := harness.RegisterAndLogin(t, "audit_user_admin")
	promoteToAdmin(t, harness, admin.UserID)

	// 自助注册：操作人就是新账号本身。
	created := auditRowsFor(t, harness, member.UserID)
	registered := requireAuditRow(t, created, "created", "audit_user_member")
	if registered.ActorID != member.UserID {
		t.Fatalf("自助注册的 actor_id = %s，期望账号本身 %s", registered.ActorID, member.UserID)
	}
	if strings.Contains(registered.Changes, "password") {
		t.Fatalf("注册记录里出现了密码相关内容: %s", registered.Changes)
	}

	// 管理员代建账号：操作人是管理员，且标注来源。
	createdUser := admin.Client.Post(URL("/users"), map[string]any{
		"username":     "audit_user_created",
		"email":        "audit_user_created@example.com",
		"display_name": "代建账号",
		"password":     SharedPassword,
	}).RequireStatus(t, 201)
	createdID := Str(createdUser.Data(t), "id")

	rows := auditRowsFor(t, harness, createdID)
	adminCreated := requireAuditRow(t, rows, "created", "audit_user_created", "admin_created")
	if adminCreated.ActorID != admin.UserID {
		t.Fatalf("管理员代建的 actor_id = %s，期望 %s", adminCreated.ActorID, admin.UserID)
	}

	// 本人改资料：updated + 新值。
	current := member.Client.Get(URL("/users/"+member.UserID)).RequireStatus(t, 200)
	updatedAt := Str(current.Data(t), "updated_at")

	member.Client.Patch(URL("/users/"+member.UserID), map[string]any{
		"display_name":        "改过的显示名",
		"expected_updated_at": updatedAt,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, member.UserID)
	requireAuditRow(t, rows, "updated", "改过的显示名")

	// 管理员重置密码：只记动作，**绝不记密码**。
	const newPassword = "a-very-secret-password-1"
	target := member.Client.Get(URL("/users/"+member.UserID)).RequireStatus(t, 200)
	targetUpdatedAt := Str(target.Data(t), "updated_at")

	admin.Client.Post(URL("/users/"+member.UserID+"/password"), map[string]any{
		"new_password":        newPassword,
		"expected_updated_at": targetUpdatedAt,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, member.UserID)
	reset := requireAuditRow(t, rows, "password_reset")
	if reset.ActorID != admin.UserID {
		t.Fatalf("重置密码的 actor_id = %s，期望 %s", reset.ActorID, admin.UserID)
	}
	for _, row := range rows {
		if strings.Contains(row.Changes, newPassword) {
			t.Fatalf("操作记录里出现了新密码明文: %s", row.Changes)
		}
	}

	// 管理员停用账号：disabled。
	target = member.Client.Get(URL("/users/"+member.UserID)).RequireStatus(t, 200)
	targetUpdatedAt = Str(target.Data(t), "updated_at")
	admin.Client.Post(URL("/users/"+member.UserID+"/disable"), map[string]any{
		"expected_updated_at": targetUpdatedAt,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, member.UserID)
	requireAuditRow(t, rows, "disabled", "disabled")

	// 负向断言 1：版本冲突的资料更新不留记录。
	//
	// 先做一次**成功**的更新把版本推进，再用旧版本重试——这样才是真的"过期版本"。
	// （直接拿创建响应的版本去 PATCH 是当前版本，当然会成功。）
	staleVersion := Str(createdUser.Data(t), "updated_at")
	admin.Client.Patch(URL("/users/"+createdID), map[string]any{
		"display_name":        "第一次改名",
		"expected_updated_at": staleVersion,
	}).RequireStatus(t, 200)

	before := countAuditRowsFor(t, harness, createdID)
	admin.Client.Patch(URL("/users/"+createdID), map[string]any{
		"display_name":        "不应生效",
		"expected_updated_at": staleVersion,
	}).RequireStatus(t, 409)
	if after := countAuditRowsFor(t, harness, createdID); after != before {
		t.Fatalf("版本冲突的更新留下了 %d 条新记录（期望 0）", after-before)
	}

	// 负向断言 2：非管理员的停用被拒，同样不留记录。
	member2 := harness.RegisterAndLogin(t, "audit_user_member2")
	before = countAuditRowsFor(t, harness, createdID)
	member2.Client.Post(URL("/users/"+createdID+"/disable"), map[string]any{
		"expected_updated_at": Str(createdUser.Data(t), "updated_at"),
	}).RequireStatus(t, 403)
	if after := countAuditRowsFor(t, harness, createdID); after != before {
		t.Fatalf("被拒的停用操作留下了 %d 条新记录（期望 0）", after-before)
	}
}
