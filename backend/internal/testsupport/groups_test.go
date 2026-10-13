package testsupport

import (
	"context"
	"net/http"
	"testing"
)

// 本文件是 Groups 域的集成测试：请求经由真实 gin 路由、真实 PostgreSQL 仓储，
// 不使用任何替身。

// TestGroupCreateWritesOwnerMembershipAtomically 验证单一写路径（backend/docs/B02 §3.2.1 保障 1）。
//
// 创建工作组必须在同一事务内写入 groups.owner_id 与所有者的 group_members 行；
// 不允许出现"有组但没有管理者"的中间状态。
func TestGroupCreateWritesOwnerMembershipAtomically(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")

	response := owner.Client.Post(URL("/groups"), map[string]any{
		"name":        "研发组",
		"description": "负责核心系统",
	})
	response.RequireStatus(t, 201)

	data := response.Data(t)
	groupID := Str(data, "id")
	if groupID == "" {
		t.Fatal("创建工作组必须返回 id")
	}
	if Str(data, "owner_id") != owner.UserID {
		t.Fatalf("owner_id = %q，期望创建者 %q", Str(data, "owner_id"), owner.UserID)
	}
	if got := Str(data, "status"); got != "active" {
		t.Fatalf("status = %q，期望 active", got)
	}
	// 创建者在本组内应为 manager。
	if got := Str(data, "membership_role"); got != "manager" {
		t.Fatalf("membership_role = %q，期望 manager", got)
	}

	// 数据库层确认两条记录同时存在。
	ctx := context.Background()
	var ownerID string
	if err := harness.Pool.QueryRow(ctx,
		`SELECT owner_id FROM groups WHERE id = $1`, groupID).Scan(&ownerID); err != nil {
		t.Fatalf("查询 groups.owner_id 失败: %v", err)
	}
	if ownerID != owner.UserID {
		t.Fatalf("groups.owner_id = %q，期望 %q", ownerID, owner.UserID)
	}

	var role, status string
	if err := harness.Pool.QueryRow(ctx,
		`SELECT role, status FROM group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, owner.UserID).Scan(&role, &status); err != nil {
		t.Fatalf("所有者成员关系必须同时写入: %v", err)
	}
	if role != "manager" || status != "active" {
		t.Fatalf("所有者成员关系 = (%s, %s)，期望 (manager, active)", role, status)
	}
}

// TestGroupOwnerCannotBeRemovedOrDemoted 验证 owner 不变式（backend/docs/B02 §3.2.1 保障 2）。
//
// 这是本次决策的核心：不引入循环复合外键，改用应用层拒绝 + 回归测试保证一致性。
func TestGroupOwnerCannotBeRemovedOrDemoted(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")

	t.Run("不能移除所有者", func(t *testing.T) {
		response := owner.Client.Delete(URL("/groups/"+groupID+"/members/"+owner.UserID), nil)
		response.RequireStatus(t, http.StatusForbidden)
		response.RequireCode(t, "permission_denied")
		response.RequireField(t, "user_id")

		// 成员关系必须仍然存在且为活跃状态。
		var status string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT status FROM group_members WHERE group_id = $1 AND user_id = $2`,
			groupID, owner.UserID).Scan(&status); err != nil {
			t.Fatalf("所有者成员关系不应被删除: %v", err)
		}
		if status != "active" {
			t.Fatalf("所有者成员状态 = %q，期望 active", status)
		}
	})

	t.Run("不能把所有者降级为普通成员", func(t *testing.T) {
		response := owner.Client.Patch(URL("/groups/"+groupID+"/members/"+owner.UserID),
			map[string]any{"role": "member"})
		response.RequireStatus(t, http.StatusForbidden)
		response.RequireCode(t, "permission_denied")

		var role string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT role FROM group_members WHERE group_id = $1 AND user_id = $2`,
			groupID, owner.UserID).Scan(&role); err != nil {
			t.Fatalf("查询成员角色失败: %v", err)
		}
		if role != "manager" {
			t.Fatalf("所有者角色 = %q，期望仍为 manager", role)
		}
	})

	t.Run("所有者可见自己的组", func(t *testing.T) {
		// 即使成员关系出现异常，所有者也不应看不到自己的组。
		owner.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, http.StatusOK)
	})
}

// TestGroupVisibilityRequiresActiveMembership 验证"审核通过前不能访问组内计划"。
func TestGroupVisibilityRequiresActiveMembership(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	outsider := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")

	t.Run("非成员看不到工作组", func(t *testing.T) {
		response := outsider.Client.Get(URL("/groups/" + groupID))
		// 返回 404 而不是 403：不应泄漏"该组存在"这一信息。
		response.RequireStatus(t, http.StatusNotFound)
		response.RequireCode(t, "group_not_found")
	})

	t.Run("非成员不能查询成员列表", func(t *testing.T) {
		response := outsider.Client.Post(URL("/groups/"+groupID+"/members/query"), map[string]any{})
		response.RequireStatus(t, http.StatusNotFound)
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		harness.Client(t).Get(URL("/groups/"+groupID)).
			RequireStatus(t, http.StatusUnauthorized).
			RequireCode(t, "authentication_required")
	})
}

// TestGroupJoinRequestLifecycle 覆盖申请 → 审核 → 成为成员的完整链路。
func TestGroupJoinRequestLifecycle(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	applicant := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")

	t.Run("提交申请返回 201", func(t *testing.T) {
		response := applicant.Client.Post(URL("/groups/"+groupID+"/join-requests"), map[string]any{})
		response.RequireStatus(t, http.StatusCreated)
		if got := Str(response.Data(t), "status"); got != "pending" {
			t.Fatalf("申请状态 = %q，期望 pending", got)
		}
	})

	t.Run("重复申请返回 409", func(t *testing.T) {
		response := applicant.Client.Post(URL("/groups/"+groupID+"/join-requests"), map[string]any{})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "conflict")
	})

	t.Run("审核前仍不能访问组内计划", func(t *testing.T) {
		applicant.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, http.StatusNotFound)
	})

	t.Run("所有者可查询待审核申请", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/"+groupID+"/join-requests/query"), map[string]any{})
		response.RequireStatus(t, http.StatusOK)

		items := response.ListData(t)
		if len(items) != 1 {
			t.Fatalf("待审核申请数量 = %d，期望 1", len(items))
		}
		if Str(items[0], "user_id") != applicant.UserID {
			t.Fatalf("申请人 = %q，期望 %q", Str(items[0], "user_id"), applicant.UserID)
		}
		// 列表必须返回真实总数，而不是恒为 0。
		if total := response.Pagination(t)["total"]; total != float64(1) {
			t.Fatalf("pagination.total = %v，期望 1", total)
		}
	})

	t.Run("非管理者不能查询申请", func(t *testing.T) {
		outsider := harness.RegisterAndLogin(t, "carol")
		response := outsider.Client.Post(URL("/groups/"+groupID+"/join-requests/query"), map[string]any{})
		response.RequireStatus(t, http.StatusNotFound)
	})

	t.Run("审核通过后成为成员", func(t *testing.T) {
		response := owner.Client.Post(
			URL("/groups/"+groupID+"/join-requests/"+applicant.UserID+"/review"),
			map[string]any{"status": "approved"})
		response.RequireStatus(t, http.StatusOK)
		if got := Str(response.Data(t), "status"); got != "approved" {
			t.Fatalf("申请状态 = %q，期望 approved", got)
		}

		// 关键：审核通过必须同时写入成员关系，否则用户"已通过但访问不了"。
		var status string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT status FROM group_members WHERE group_id = $1 AND user_id = $2`,
			groupID, applicant.UserID).Scan(&status); err != nil {
			t.Fatalf("审核通过必须写入成员关系: %v", err)
		}
		if status != "active" {
			t.Fatalf("成员状态 = %q，期望 active", status)
		}

		// 现在应能访问组内计划。
		applicant.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, http.StatusOK)
	})

	t.Run("重复审核返回 409", func(t *testing.T) {
		// 早期实现此处漏捕 IntegrityError，实测返回 500。
		response := owner.Client.Post(
			URL("/groups/"+groupID+"/join-requests/"+applicant.UserID+"/review"),
			map[string]any{"status": "approved"})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "conflict")
	})

	t.Run("审核结果只能是 approved 或 rejected", func(t *testing.T) {
		other := harness.RegisterAndLogin(t, "dave")
		other.Client.Post(URL("/groups/"+groupID+"/join-requests"), map[string]any{}).
			RequireStatus(t, http.StatusCreated)

		response := owner.Client.Post(
			URL("/groups/"+groupID+"/join-requests/"+other.UserID+"/review"),
			map[string]any{"status": "maybe"})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireCode(t, "validation_error")
		response.RequireField(t, "status")
	})
}

// TestGroupJoinRequestCanBeReappliedAfterRejection 覆盖被拒绝后重新申请。
func TestGroupJoinRequestCanBeReappliedAfterRejection(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	applicant := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")

	applicant.Client.Post(URL("/groups/"+groupID+"/join-requests"), map[string]any{}).
		RequireStatus(t, http.StatusCreated)
	owner.Client.Post(URL("/groups/"+groupID+"/join-requests/"+applicant.UserID+"/review"),
		map[string]any{"status": "rejected"}).RequireStatus(t, http.StatusOK)

	// 被拒绝后应能重新申请：申请记录唯一，走"重新开启"而不是插入新行。
	response := applicant.Client.Post(URL("/groups/"+groupID+"/join-requests"), map[string]any{})
	response.RequireStatus(t, http.StatusCreated)
	if got := Str(response.Data(t), "status"); got != "pending" {
		t.Fatalf("重新申请后的状态 = %q，期望 pending", got)
	}

	// 数据库中仍只有一行申请记录（唯一约束不允许出现两行）。
	var count int
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM group_join_requests WHERE group_id = $1 AND user_id = $2`,
		groupID, applicant.UserID).Scan(&count); err != nil {
		t.Fatalf("统计申请记录失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("申请记录数量 = %d，期望 1（唯一约束）", count)
	}
}

// TestGroupMemberManagement 覆盖添加、提升、移除成员。
func TestGroupMemberManagement(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	member := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")

	t.Run("重复添加返回 409", func(t *testing.T) {
		harness.AddGroupMember(t, owner, groupID, member.UserID)
		response := owner.Client.Post(URL("/groups/"+groupID+"/members"),
			map[string]any{"user_id": member.UserID})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "duplicate_member")
		response.RequireField(t, "user_id")
	})

	t.Run("提升为组管理员", func(t *testing.T) {
		response := owner.Client.Patch(URL("/groups/"+groupID+"/members/"+member.UserID),
			map[string]any{"role": "manager"})
		response.RequireStatus(t, http.StatusOK)
		if got := Str(response.Data(t), "role"); got != "manager" {
			t.Fatalf("role = %q，期望 manager", got)
		}
	})

	t.Run("组管理员可以管理其他成员", func(t *testing.T) {
		third := harness.RegisterAndLogin(t, "carol")
		response := member.Client.Post(URL("/groups/"+groupID+"/members"),
			map[string]any{"user_id": third.UserID})
		response.RequireStatus(t, http.StatusCreated)
	})

	t.Run("普通成员不能管理成员", func(t *testing.T) {
		plain := harness.RegisterAndLogin(t, "dave")
		harness.AddGroupMember(t, owner, groupID, plain.UserID)

		response := plain.Client.Post(URL("/groups/"+groupID+"/members"),
			map[string]any{"user_id": owner.UserID})
		// 无管理权限时统一返回 404，避免泄漏组内结构。
		response.RequireStatus(t, http.StatusNotFound)
	})

	t.Run("移除成员是软移除", func(t *testing.T) {
		response := owner.Client.Delete(URL("/groups/"+groupID+"/members/"+member.UserID), nil)
		response.RequireStatus(t, http.StatusNoContent)

		// 需求文档 5.2：成员移除后个人数据保留，成员关系置为 removed 而不是删除。
		var status string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT status FROM group_members WHERE group_id = $1 AND user_id = $2`,
			groupID, member.UserID).Scan(&status); err != nil {
			t.Fatalf("成员关系应保留为软移除: %v", err)
		}
		if status != "removed" {
			t.Fatalf("成员状态 = %q，期望 removed", status)
		}

		// 被移除后不再能看到组内计划。
		member.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, http.StatusNotFound)
	})

	t.Run("添加不存在用户返回 404", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/"+groupID+"/members"),
			map[string]any{"user_id": "01a1290b-0ae8-7046-9106-30f2d07815af"})
		response.RequireStatus(t, http.StatusNotFound)
		response.RequireCode(t, "user_not_found")
	})

	t.Run("非法 UUID 参数返回 400", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/not-a-uuid/members"),
			map[string]any{"user_id": member.UserID})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_request")
	})
}

// TestGroupUpdateUsesVersionConflict 验证 PATCH 的原子 CAS 必须提交版本。
func TestGroupUpdateUsesVersionConflict(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")

	current := owner.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, http.StatusOK)
	updatedAt := Str(current.Data(t), "updated_at")
	if updatedAt == "" {
		t.Fatal("详情必须返回 updated_at")
	}

	t.Run("省略 expected_updated_at 被拒绝", func(t *testing.T) {
		// 早期实现把该字段设为可选，省略即跳过校验，使版本协议静默退化。
		response := owner.Client.Patch(URL("/groups/"+groupID), map[string]any{"name": "新名字"})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireCode(t, "validation_error")
	})

	t.Run("版本正确时更新成功且 updated_at 推进", func(t *testing.T) {
		response := owner.Client.Patch(URL("/groups/"+groupID), map[string]any{
			"name":                "新名字",
			"expected_updated_at": updatedAt,
		})
		response.RequireStatus(t, http.StatusOK)

		data := response.Data(t)
		if Str(data, "name") != "新名字" {
			t.Fatalf("name = %q，期望 新名字", Str(data, "name"))
		}
		if Str(data, "updated_at") == updatedAt {
			t.Fatal("更新必须推进 updated_at，否则版本协议失效")
		}
	})

	t.Run("版本过期返回 409 version_conflict", func(t *testing.T) {
		response := owner.Client.Patch(URL("/groups/"+groupID), map[string]any{
			"name":                "又改一次",
			"expected_updated_at": updatedAt,
		})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "version_conflict")
	})

	t.Run("对象不存在返回 404 而不是版本冲突", func(t *testing.T) {
		response := owner.Client.Patch(
			URL("/groups/01a1290b-0ae8-7046-9106-30f2d07815af"),
			map[string]any{"name": "x", "expected_updated_at": updatedAt})
		response.RequireStatus(t, http.StatusNotFound)
		response.RequireCode(t, "group_not_found")
	})
}

// TestGroupListPaginationAndSorting 验证分页在服务端强制、排序白名单生效。
func TestGroupListPaginationAndSorting(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")

	for _, name := range []string{"alpha", "beta", "gamma"} {
		harness.CreateGroup(t, owner, name)
	}

	t.Run("默认分页与真实总数", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/query"), map[string]any{})
		response.RequireStatus(t, http.StatusOK)

		pagination := response.Pagination(t)
		if pagination["total"] != float64(3) {
			t.Fatalf("total = %v，期望 3", pagination["total"])
		}
		if pagination["page"] != float64(1) || pagination["page_size"] != float64(20) {
			t.Fatalf("默认分页 = %v，期望 page=1 page_size=20", pagination)
		}
	})

	t.Run("分页在 SQL 层生效", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/query"), map[string]any{
			"page": 1, "page_size": 2, "sort": "name",
		})
		response.RequireStatus(t, http.StatusOK)

		if items := response.ListData(t); len(items) != 2 {
			t.Fatalf("第一页条目数 = %d，期望 2", len(items))
		}
		pagination := response.Pagination(t)
		if pagination["has_next"] != true {
			t.Fatalf("has_next = %v，期望 true", pagination["has_next"])
		}
	})

	t.Run("page_size 超限被拒绝", func(t *testing.T) {
		// 早期实现实测 page_size=500 被接受并原样回显。
		response := owner.Client.Post(URL("/groups/query"), map[string]any{"page_size": 500})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_request")
		response.RequireField(t, "page_size")
	})

	t.Run("page 为 0 被拒绝", func(t *testing.T) {
		// 早期实现实测 page=0 返回 200。
		response := owner.Client.Post(URL("/groups/query"), map[string]any{"page": 0})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_request")
		response.RequireField(t, "page")
	})

	t.Run("未知排序字段被拒绝", func(t *testing.T) {
		// 早期实现实测 sort 校验后被丢弃，SQL 硬编码 ORDER BY。
		response := owner.Client.Post(URL("/groups/query"), map[string]any{"sort": "nonsense"})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_request")
		response.RequireField(t, "sort")
	})

	t.Run("状态筛选生效", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/query"), map[string]any{"status": "disabled"})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(0) {
			t.Fatalf("disabled 组数量 = %v，期望 0", total)
		}
	})

	t.Run("非法状态被拒绝", func(t *testing.T) {
		response := owner.Client.Post(URL("/groups/query"), map[string]any{"status": "archived"})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireField(t, "status")
	})
}

// TestGroupSortIsStableAcrossPages 验证排序追加 id 次级键后翻页不重不漏。
func TestGroupSortIsStableAcrossPages(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")

	// 同名工作组：没有 id 次级键时排序不稳定，会出现跨页重复或漏出。
	for i := 0; i < 6; i++ {
		harness.CreateGroup(t, owner, "same-name")
	}

	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		response := owner.Client.Post(URL("/groups/query"), map[string]any{
			"page": page, "page_size": 2, "sort": "name",
		})
		response.RequireStatus(t, http.StatusOK)

		for _, item := range response.ListData(t) {
			id := Str(item, "id")
			if seen[id] {
				t.Fatalf("工作组 %s 在翻页中重复出现（第 %d 页）", id, page)
			}
			seen[id] = true
		}
	}

	if len(seen) != 6 {
		t.Fatalf("翻页共取到 %d 个不同工作组，期望 6（存在遗漏）", len(seen))
	}
}

// TestGroupDescriptionCanBeCleared 覆盖"显式置空"与"未提供"的区别。
func TestGroupDescriptionCanBeCleared(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")

	created := owner.Client.Post(URL("/groups"), map[string]any{
		"name": "研发组", "description": "初始描述",
	})
	created.RequireStatus(t, http.StatusCreated)
	groupID := Str(created.Data(t), "id")

	updatedAt := Str(owner.Client.Get(URL("/groups/"+groupID)).Data(t), "updated_at")

	t.Run("仅改名称时描述保留", func(t *testing.T) {
		response := owner.Client.Patch(URL("/groups/"+groupID), map[string]any{
			"name": "改名", "expected_updated_at": updatedAt,
		})
		response.RequireStatus(t, http.StatusOK)
		if got := response.Data(t)["description"]; got != "初始描述" {
			t.Fatalf("description = %v，期望保留初始描述", got)
		}
		updatedAt = Str(response.Data(t), "updated_at")
	})

	t.Run("显式传空字符串清空描述", func(t *testing.T) {
		response := owner.Client.Patch(URL("/groups/"+groupID), map[string]any{
			"description": "", "expected_updated_at": updatedAt,
		})
		response.RequireStatus(t, http.StatusOK)
		if got := response.Data(t)["description"]; got != nil {
			t.Fatalf("description = %v，期望 null（已清空）", got)
		}
	})
}
