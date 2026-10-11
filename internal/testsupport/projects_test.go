package testsupport

import (
	"context"
	"net/http"
	"testing"
)

// 本文件是 Projects 域的集成测试。

// TestProjectCreateRequiresActiveGroupMembership 验证创建项目的前置条件。
func TestProjectCreateRequiresActiveGroupMembership(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	outsider := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")

	t.Run("非组员不能在该组下创建项目", func(t *testing.T) {
		response := outsider.Client.Post(URL("/projects"), map[string]any{
			"name": "外部项目", "group_id": groupID,
			"start_date": "2026-10-01", "end_date": "2026-10-31",
		})
		response.RequireStatus(t, http.StatusForbidden)
		response.RequireCode(t, "permission_denied")
		response.RequireField(t, "group_id")
	})

	t.Run("归属组不存在返回校验错误", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects"), map[string]any{
			"name": "无效组项目", "group_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
			"start_date": "2026-10-01", "end_date": "2026-10-31",
		})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "group_id")
	})

	t.Run("创建者成为持有者且默认为私有未开始", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects"), map[string]any{
			"name": "核心系统迁移", "group_id": groupID,
			"start_date": "2026-10-01", "end_date": "2026-10-31",
		})
		response.RequireStatus(t, http.StatusCreated)

		data := response.Data(t)
		if got := Str(data, "status"); got != "not_started" {
			t.Fatalf("status = %q，期望 not_started", got)
		}
		if got := Str(data, "visibility"); got != "private" {
			t.Fatalf("visibility = %q，期望 private", got)
		}
		if got := Str(data, "membership_role"); got != "owner" {
			t.Fatalf("membership_role = %q，期望 owner", got)
		}
		// 派生字段必须存在且为 0。
		if got := data["progress"]; got != float64(0) {
			t.Fatalf("progress = %v，期望 0", got)
		}
		if got := data["overdue_task_count"]; got != float64(0) {
			t.Fatalf("overdue_task_count = %v，期望 0", got)
		}
	})

	t.Run("日期区间倒置返回 400", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects"), map[string]any{
			"name": "倒置", "group_id": groupID,
			"start_date": "2026-10-31", "end_date": "2026-10-01",
		})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_date_range")
		response.RequireField(t, "end_date")
	})
}

// TestProjectProgressExcludesCancelledTasks 验证派生进度规则（需求文档 5.3）。
//
// 规则：已取消任务不参与计算；无有效任务时为 0；进度为有效任务的简单平均值。
func TestProjectProgressExcludesCancelledTasks(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	// 新建项目没有任务时进度为 0。
	detail := owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
	if got := detail.Data(t)["progress"]; got != float64(0) {
		t.Fatalf("无任务时 progress = %v，期望 0", got)
	}

	// 三个任务：50%、100%、90%（最后一个将被取消）。
	taskA := harness.CreateTask(t, owner, projectID, "A", "2026-10-01", "2026-10-05")
	taskB := harness.CreateTask(t, owner, projectID, "B", "2026-10-01", "2026-10-05")
	taskC := harness.CreateTask(t, owner, projectID, "C", "2026-10-01", "2026-10-05")

	setProgress := func(taskID string, progress int) {
		t.Helper()
		current := owner.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, http.StatusOK)
		response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"progress":            progress,
			"expected_updated_at": Str(current.Data(t), "updated_at"),
		})
		response.RequireStatus(t, http.StatusOK)
	}

	setProgress(taskA, 50)
	setProgress(taskB, 100)

	t.Run("平均值计算正确", func(t *testing.T) {
		detail := owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
		// (50 + 100 + 0) / 3 = 50
		if got := detail.Data(t)["progress"]; got != float64(50) {
			t.Fatalf("progress = %v，期望 50", got)
		}
	})

	t.Run("进度自动推导状态", func(t *testing.T) {
		// 需求文档 5.4：1%–99% 对应进行中，100% 对应已完成。
		a := owner.Client.Get(URL("/tasks/"+taskA)).RequireStatus(t, http.StatusOK)
		if got := Str(a.Data(t), "status"); got != "in_progress" {
			t.Fatalf("进度 50 的任务状态 = %q，期望 in_progress", got)
		}
		b := owner.Client.Get(URL("/tasks/"+taskB)).RequireStatus(t, http.StatusOK)
		if got := Str(b.Data(t), "status"); got != "completed" {
			t.Fatalf("进度 100 的任务状态 = %q，期望 completed", got)
		}
	})

	t.Run("已取消任务不参与进度平均", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/"+taskC)).RequireStatus(t, http.StatusOK)
		owner.Client.Patch(URL("/tasks/"+taskC), map[string]any{
			"status":              "cancelled",
			"progress":            90,
			"expected_updated_at": Str(current.Data(t), "updated_at"),
		}).RequireStatus(t, http.StatusOK)

		detail := owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
		// (50 + 100) / 2 = 75，不含已取消的 90
		if got := detail.Data(t)["progress"]; got != float64(75) {
			t.Fatalf("progress = %v，期望 75（已取消任务不得参与）", got)
		}
	})

	t.Run("逻辑删除的任务不参与进度平均", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/"+taskA)).RequireStatus(t, http.StatusOK)
		owner.Client.Delete(URL("/tasks/"+taskA), map[string]any{
			"expected_updated_at": Str(current.Data(t), "updated_at"),
		}).RequireStatus(t, http.StatusNoContent)

		detail := owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
		// 只剩 B(100) 有效
		if got := detail.Data(t)["progress"]; got != float64(100) {
			t.Fatalf("progress = %v，期望 100（已删除任务不得参与）", got)
		}
	})
}

// TestProjectOverdueTaskCountIsDerived 验证逾期任务数是派生字段。
func TestProjectOverdueTaskCountIsDerived(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")

	// harness 的固定时钟是 2026-10-11，因此 2026-10-05 已过期。
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	harness.CreateTask(t, owner, projectID, "已过期", "2026-10-01", "2026-10-05")
	harness.CreateTask(t, owner, projectID, "未到期", "2026-10-01", "2026-10-30")

	detail := owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
	if got := detail.Data(t)["overdue_task_count"]; got != float64(1) {
		t.Fatalf("overdue_task_count = %v，期望 1", got)
	}
}

// TestProjectArchiveMakesProjectReadOnly 验证归档项目只读。
//
// 这是早期实现的实测缺陷：`_can_manage_task` 不检查 project.status，
// 归档项目仍可被修改。
func TestProjectArchiveMakesProjectReadOnly(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	taskID := harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	t.Run("归档成功", func(t *testing.T) {
		current := owner.Client.Get(URL("/projects/" + projectID)).Data(t)
		response := owner.Client.Post(URL("/projects/"+projectID+"/archive"), map[string]any{
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusOK)

		data := response.Data(t)
		if got := Str(data, "status"); got != "archived" {
			t.Fatalf("status = %q，期望 archived", got)
		}
		if data["archived_at"] == nil {
			t.Fatal("归档必须写入 archived_at")
		}
	})

	t.Run("归档后不能修改项目", func(t *testing.T) {
		current := owner.Client.Get(URL("/projects/" + projectID)).Data(t)
		response := owner.Client.Patch(URL("/projects/"+projectID), map[string]any{
			"name":                "改名",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "conflict")
		response.RequireField(t, "project_id")
	})

	t.Run("归档后不能修改任务", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"name":                "改名",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusConflict)
	})

	t.Run("归档后不能创建任务", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
			"name": "新任务", "start_date": "2026-10-01", "end_date": "2026-10-05",
		})
		response.RequireStatus(t, http.StatusConflict)
	})

	t.Run("归档后不能删除任务", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Delete(URL("/tasks/"+taskID), map[string]any{
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusConflict)
	})

	t.Run("归档后不能改甘特图日期", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+taskID+"/dates"), map[string]any{
			"start_date": "2026-10-02", "end_date": "2026-10-06",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusConflict)
	})

	t.Run("归档后仍可读取", func(t *testing.T) {
		owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
		owner.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, http.StatusOK)
	})
}

// TestProjectListFiltersAndArchivedExclusion 覆盖筛选与归档默认排除。
func TestProjectListFiltersAndArchivedExclusion(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	member := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")
	harness.AddGroupMember(t, owner, groupID, member.UserID)

	projectA := harness.CreateProject(t, owner, groupID, "Alpha 迁移")
	projectB := harness.CreateProject(t, owner, groupID, "Beta 上线")

	// 让 bob 成为 projectB 的成员，用于验证成员可见性。
	owner.Client.Post(URL("/projects/"+projectB+"/members"),
		map[string]any{"user_id": member.UserID}).RequireStatus(t, http.StatusCreated)

	t.Run("私有项目对非成员不可见", func(t *testing.T) {
		member.Client.Get(URL("/projects/"+projectA)).
			RequireStatus(t, http.StatusNotFound).
			RequireCode(t, "project_not_found")
	})

	t.Run("项目成员可见", func(t *testing.T) {
		member.Client.Get(URL("/projects/"+projectB)).RequireStatus(t, http.StatusOK)
	})

	t.Run("按持有者筛选", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/query"), map[string]any{
			"owner_id": owner.UserID,
		})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(2) {
			t.Fatalf("owner 筛选总数 = %v，期望 2", total)
		}
	})

	t.Run("按非持有者筛选返回空", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/query"), map[string]any{
			"owner_id": member.UserID,
		})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(0) {
			t.Fatalf("member 作为 owner 的筛选总数 = %v，期望 0", total)
		}
	})

	t.Run("按名称搜索", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/query"), map[string]any{"name": "Alpha"})
		response.RequireStatus(t, http.StatusOK)
		items := response.ListData(t)
		if len(items) != 1 || Str(items[0], "id") != projectA {
			t.Fatalf("名称搜索结果不符合预期: %v", items)
		}
	})

	t.Run("名称搜索中的通配符按字面量处理", func(t *testing.T) {
		// 未转义时 "%" 会匹配全部记录。
		response := owner.Client.Post(URL("/projects/query"), map[string]any{"name": "%"})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(0) {
			t.Fatalf("通配符搜索总数 = %v，期望 0（LIKE 通配符必须被转义）", total)
		}
	})

	t.Run("按组与状态组合筛选", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/query"), map[string]any{
			"group_id": groupID,
			"status":   "not_started",
		})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(2) {
			t.Fatalf("组合筛选总数 = %v，期望 2", total)
		}
	})

	t.Run("归档项目默认从列表排除", func(t *testing.T) {
		current := owner.Client.Get(URL("/projects/" + projectB)).Data(t)
		owner.Client.Post(URL("/projects/"+projectB+"/archive"), map[string]any{
			"expected_updated_at": Str(current, "updated_at"),
		}).RequireStatus(t, http.StatusOK)

		response := owner.Client.Post(URL("/projects/query"), map[string]any{})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(1) {
			t.Fatalf("默认列表总数 = %v，期望 1（归档项目应被排除）", total)
		}

		// 显式请求时可见（需求文档 5.3：历史数据可查询）。
		response = owner.Client.Post(URL("/projects/query"), map[string]any{"include_archived": true})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(2) {
			t.Fatalf("include_archived 总数 = %v，期望 2", total)
		}
	})
}

// TestProjectMemberInvariants 覆盖项目成员不变式。
func TestProjectMemberInvariants(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	member := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	t.Run("重复添加返回 409", func(t *testing.T) {
		owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": member.UserID}).RequireStatus(t, http.StatusCreated)
		response := owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": member.UserID})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "duplicate_member")
	})

	t.Run("不能降级唯一持有者", func(t *testing.T) {
		response := owner.Client.Patch(URL("/projects/"+projectID+"/members/"+owner.UserID),
			map[string]any{"role": "member"})
		response.RequireStatus(t, http.StatusForbidden)
		response.RequireCode(t, "permission_denied")
	})

	t.Run("不能移除唯一持有者", func(t *testing.T) {
		response := owner.Client.Delete(URL("/projects/"+projectID+"/members/"+owner.UserID), nil)
		response.RequireStatus(t, http.StatusForbidden)
	})

	t.Run("普通项目成员不能管理成员", func(t *testing.T) {
		third := harness.RegisterAndLogin(t, "carol")
		response := member.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": third.UserID})
		// member 只有 PermWrite，没有 PermManage。
		response.RequireStatus(t, http.StatusNotFound)
	})

	t.Run("添加不存在用户返回 404", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": "01a1290b-0ae8-7046-9106-30f2d07815af"})
		response.RequireStatus(t, http.StatusNotFound)
		response.RequireCode(t, "user_not_found")
	})

	t.Run("添加被停用用户被拒绝", func(t *testing.T) {
		disabled := harness.RegisterAndLogin(t, "dave")
		if _, err := harness.Pool.Exec(context.Background(),
			`UPDATE users SET status = 'disabled', updated_at = now() WHERE id = $1`,
			disabled.UserID); err != nil {
			t.Fatalf("停用用户失败: %v", err)
		}

		response := owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": disabled.UserID})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "user_id")
	})

	t.Run("成员可以来自其他工作组", func(t *testing.T) {
		// 需求文档 5.3：项目成员可以来自不同工作组。
		other := harness.RegisterAndLogin(t, "erin")
		// erin 不在 projectID 的归属组里，但可以直接加入项目。
		response := owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": other.UserID})
		response.RequireStatus(t, http.StatusCreated)
		other.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
	})

	// 转移持有者放在最后：它会把 owner 降级，因此前面所有"以 owner 身份操作"的子用例
	// 都必须在它之前跑完。这也正是早期版本的一个隐患——把转移当成"提升"的副作用，
	// 会让同一个用例里后续步骤的前提悄悄失效。
	t.Run("持有者可以转移", func(t *testing.T) {
		transferred := owner.Client.Patch(URL("/projects/"+projectID+"/members/"+member.UserID),
			map[string]any{"role": "owner"})
		transferred.RequireStatus(t, http.StatusOK)
		if got := Str(transferred.Data(t), "role"); got != "owner" {
			t.Fatalf("转移后的角色 = %q，期望 owner", got)
		}

		// 不变式：任何时刻恰好一个持有者。
		var owners int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM project_members WHERE project_id = $1 AND role = 'owner'`,
			projectID).Scan(&owners); err != nil {
			t.Fatalf("统计持有者失败: %v", err)
		}
		if owners != 1 {
			t.Fatalf("转移后持有者数量 = %d，期望恰好 1", owners)
		}

		// 新持有者获得管理权，原持有者降为普通成员并失去管理权。
		third := harness.RegisterAndLogin(t, "frank")
		member.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": third.UserID}).RequireStatus(t, http.StatusCreated)

		owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": third.UserID}).
			RequireStatus(t, http.StatusNotFound)

		// 已是持有者时再次请求转移：冲突，而不是"成功但没有变化"。
		repeat := member.Client.Patch(URL("/projects/"+projectID+"/members/"+member.UserID),
			map[string]any{"role": "owner"})
		repeat.RequireStatus(t, http.StatusConflict)
		repeat.RequireField(t, "role")
	})

	t.Run("不能把持有者转给已停用用户", func(t *testing.T) {
		// 转移目标必须是有效用户：否则项目会立刻进入"谁都改不了"的状态。
		pending := harness.RegisterAndLogin(t, "grace")
		member.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": pending.UserID}).RequireStatus(t, http.StatusCreated)
		if _, err := harness.Pool.Exec(context.Background(),
			`UPDATE users SET status = 'disabled', updated_at = now() WHERE id = $1`,
			pending.UserID); err != nil {
			t.Fatalf("停用用户失败: %v", err)
		}

		response := member.Client.Patch(URL("/projects/"+projectID+"/members/"+pending.UserID),
			map[string]any{"role": "owner"})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "user_id")

		var owners int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM project_members WHERE project_id = $1 AND role = 'owner'`,
			projectID).Scan(&owners); err != nil {
			t.Fatalf("统计持有者失败: %v", err)
		}
		if owners != 1 {
			t.Fatalf("被拒绝的转移不应改变持有者数量，实际 %d", owners)
		}
	})

	t.Run("转移给非成员必须整体回滚", func(t *testing.T) {
		// 这是"必须放在同一事务里"的直接证据：转移由两条语句组成
		// （先降现任、再升目标）。若第二条失败而第一条已经提交，
		// 项目就会处于"没有持有者"的状态——没有任何人能再管理它。
		nonMember := harness.RegisterAndLogin(t, "heidi")

		response := member.Client.Patch(URL("/projects/"+projectID+"/members/"+nonMember.UserID),
			map[string]any{"role": "owner"})
		response.RequireStatus(t, http.StatusNotFound)
		response.RequireField(t, "user_id")

		var owners int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM project_members WHERE project_id = $1 AND role = 'owner'`,
			projectID).Scan(&owners); err != nil {
			t.Fatalf("统计持有者失败: %v", err)
		}
		if owners != 1 {
			t.Fatalf("失败的转移必须整体回滚：持有者数量 = %d，期望仍为 1", owners)
		}
	})
}

// TestProjectGroupVisibilityLevels 覆盖三种共享级别的可见性差异。
func TestProjectGroupVisibilityLevels(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupMember := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")
	harness.AddGroupMember(t, owner, groupID, groupMember.UserID)
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	// 组员加一个有共享设置的任务，用于验证"任务已共享"才进组内视图。
	taskID := harness.CreateTask(t, owner, projectID, "共享任务", "2026-10-01", "2026-10-05")
	current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
	owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"is_shared":           true,
		"expected_updated_at": Str(current, "updated_at"),
	}).RequireStatus(t, http.StatusOK)

	t.Run("私有项目对组员不可见", func(t *testing.T) {
		groupMember.Client.Get(URL("/projects/"+projectID)).
			RequireStatus(t, http.StatusNotFound)
	})

	t.Run("组内只读：可见但不可改", func(t *testing.T) {
		setVisibility(t, owner, projectID, "group_readonly")
		groupMember.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)

		current := groupMember.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := groupMember.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"name":                "改共享任务",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusForbidden)

		// 但也不能修改项目本身。
		currentProject := groupMember.Client.Get(URL("/projects/" + projectID)).Data(t)
		groupMember.Client.Patch(URL("/projects/"+projectID), map[string]any{
			"name":                "改项目",
			"expected_updated_at": Str(currentProject, "updated_at"),
		}).RequireStatus(t, http.StatusForbidden)
	})

	t.Run("组内可编辑：可改已共享任务，仍不能改项目共享权限", func(t *testing.T) {
		setVisibility(t, owner, projectID, "group_editable")

		current := groupMember.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := groupMember.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"name":                "组内编辑成功",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusOK)

		// 需求文档 4.3：组内可编辑允许修改共享任务，但不能修改项目共享权限。
		currentProject := groupMember.Client.Get(URL("/projects/" + projectID)).Data(t)
		groupMember.Client.Patch(URL("/projects/"+projectID), map[string]any{
			"visibility":          "private",
			"expected_updated_at": Str(currentProject, "updated_at"),
		}).RequireStatus(t, http.StatusForbidden)
	})

	t.Run("未共享任务不进组内视图", func(t *testing.T) {
		setVisibility(t, owner, projectID, "group_editable")
		unshared := harness.CreateTask(t, owner, projectID, "未共享任务", "2026-10-01", "2026-10-05")

		groupMember.Client.Get(URL("/tasks/"+unshared)).
			RequireStatus(t, http.StatusNotFound)

		response := groupMember.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
			map[string]any{})
		response.RequireStatus(t, http.StatusOK)
		for _, item := range response.ListData(t) {
			if Str(item, "id") == unshared {
				t.Fatal("未共享任务不得出现在组内视图中")
			}
		}
	})
}

// setVisibility 以当前版本把项目共享级别改为指定值。
func setVisibility(t *testing.T, actor Authenticated, projectID, visibility string) {
	t.Helper()

	current := actor.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, http.StatusOK)
	response := actor.Client.Patch(URL("/projects/"+projectID), map[string]any{
		"visibility":          visibility,
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	})
	response.RequireStatus(t, http.StatusOK)
}

// TestProjectUpdateDateRangeUsesMergedValues 验证部分更新时日期区间用合并后的值校验。
func TestProjectUpdateDateRangeUsesMergedValues(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	t.Run("只改 end_date 为非法值时被拒绝", func(t *testing.T) {
		current := owner.Client.Get(URL("/projects/" + projectID)).Data(t)
		// 只提供 end_date，且早于已有的 start_date（2026-10-01）。
		// 若实现只用请求体里的 start_date（为空）去比较，就会漏判。
		response := owner.Client.Patch(URL("/projects/"+projectID), map[string]any{
			"end_date":            "2026-09-01",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_date_range")
	})

	t.Run("只改 end_date 为合法值时成功", func(t *testing.T) {
		current := owner.Client.Get(URL("/projects/" + projectID)).Data(t)
		response := owner.Client.Patch(URL("/projects/"+projectID), map[string]any{
			"end_date":            "2026-11-30",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusOK)
		if got := Str(response.Data(t), "end_date"); got != "2026-11-30" {
			t.Fatalf("end_date = %q，期望 2026-11-30", got)
		}
		// start_date 必须保持不变。
		if got := Str(response.Data(t), "start_date"); got != "2026-10-01" {
			t.Fatalf("start_date = %q，期望保持 2026-10-01", got)
		}
	})
}
