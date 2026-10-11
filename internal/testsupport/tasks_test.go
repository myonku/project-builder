package testsupport

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// 本文件是 Tasks 域的集成测试。

// TestTaskCombinedMembershipFilter 是本项目最重要的防回归用例。
//
// 历史缺陷（docs/01 5.3）：`owner_id` 与 `assignee_id` 同时给出时，
// Python 版本把两个谓词放在同一张成员表的同一行上，要求同一个人既是 owner
// 又是 assignee，结果恒为空——实测"单条件各返回 1 条，组合返回 0 条"。
//
// 正确做法是每个成员条件各用一个别名 JOIN（本实现用独立 EXISTS 子查询达成同一效果）。
func TestTaskCombinedMembershipFilter(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	assignee := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	// 两人都必须是项目成员，才能成为任务成员。
	owner.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": assignee.UserID}).RequireStatus(t, http.StatusCreated)

	taskID := harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	// owner 由创建流程自动成为任务持有者；assignee 作为普通成员加入。
	owner.Client.Post(URL("/tasks/"+taskID+"/members"),
		map[string]any{"user_id": assignee.UserID}).RequireStatus(t, http.StatusCreated)

	query := func(filter map[string]any) int {
		t.Helper()
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks/query"), filter)
		response.RequireStatus(t, http.StatusOK)
		total, _ := response.Pagination(t)["total"].(float64)
		return int(total)
	}

	t.Run("单条件各自命中", func(t *testing.T) {
		if got := query(map[string]any{"owner_id": owner.UserID}); got != 1 {
			t.Fatalf("owner_id 单条件 = %d，期望 1", got)
		}
		if got := query(map[string]any{"assignee_id": assignee.UserID}); got != 1 {
			t.Fatalf("assignee_id 单条件 = %d，期望 1", got)
		}
		if got := query(map[string]any{"owner_id": assignee.UserID}); got != 0 {
			t.Fatalf("assignee 不是 owner，应返回 0，实际 %d", got)
		}
	})

	t.Run("组合条件必须命中而不是恒为空", func(t *testing.T) {
		// 这是历史缺陷的核心断言：不同用户分别持有与承担同一任务时，组合筛选必须能查到。
		got := query(map[string]any{
			"owner_id":    owner.UserID,
			"assignee_id": assignee.UserID,
		})
		if got != 1 {
			t.Fatalf("owner+assignee 组合筛选 = %d，期望 1（历史缺陷表现为 0）", got)
		}
	})

	t.Run("组合条件指向同一人且其同时是两者时命中", func(t *testing.T) {
		// owner 同时也是任务成员，因此既是 owner 也是 assignee。
		got := query(map[string]any{
			"owner_id":    owner.UserID,
			"assignee_id": owner.UserID,
		})
		if got != 1 {
			t.Fatalf("同一人的 owner+assignee = %d，期望 1", got)
		}
	})
}

// TestTaskSoftDeleteIsConsistentBetweenDetailAndList 验证逻辑删除在单查与列表上行为一致。
//
// 历史缺陷（docs/01 5.5）：Python 版本列表过滤了 deleted_at，单查没有，
// 导致已删除任务仍可被读取。
func TestTaskSoftDeleteIsConsistentBetweenDetailAndList(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	kept := harness.CreateTask(t, owner, projectID, "保留", "2026-10-01", "2026-10-05")
	removed := harness.CreateTask(t, owner, projectID, "删除", "2026-10-01", "2026-10-05")

	current := owner.Client.Get(URL("/tasks/" + removed)).Data(t)
	owner.Client.Delete(URL("/tasks/"+removed), map[string]any{
		"expected_updated_at": Str(current, "updated_at"),
	}).RequireStatus(t, http.StatusNoContent)

	t.Run("单查已删除任务返回 404", func(t *testing.T) {
		owner.Client.Get(URL("/tasks/"+removed)).
			RequireStatus(t, http.StatusNotFound).
			RequireCode(t, "task_not_found")
	})

	t.Run("列表默认排除已删除任务", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks/query"), map[string]any{})
		response.RequireStatus(t, http.StatusOK)

		items := response.ListData(t)
		if len(items) != 1 || Str(items[0], "id") != kept {
			t.Fatalf("列表应只包含未删除任务，实际: %v", items)
		}
	})

	t.Run("显式 include_deleted 时可见", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
			map[string]any{"include_deleted": true})
		response.RequireStatus(t, http.StatusOK)
		if total := response.Pagination(t)["total"]; total != float64(2) {
			t.Fatalf("include_deleted 总数 = %v，期望 2", total)
		}
	})

	t.Run("已删除任务不能被更新", func(t *testing.T) {
		// 单查路径排除了已删除任务，因此更新也应看不到它。
		response := owner.Client.Patch(URL("/tasks/"+removed), map[string]any{
			"name":                "改名",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusNotFound)
	})
}

// TestTaskCopyIdempotency 验证 Idempotency-Key 真正去重。
//
// 历史缺陷（docs/01 5.6）：Python 版本完全未实现该机制，
// 同一 key 连续两次 copy 产生两个任务。
func TestTaskCopyIdempotency(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	taskID := harness.CreateTask(t, owner, projectID, "源任务", "2026-10-01", "2026-10-05")

	const key = "copy-key-0001"

	t.Run("首次请求创建新任务并返回 201", func(t *testing.T) {
		response := owner.Client.PostWithHeaders(URL("/tasks/"+taskID+"/copy"),
			map[string]any{}, map[string]string{"Idempotency-Key": key})
		response.RequireStatus(t, http.StatusCreated)
		if Str(response.Data(t), "id") == "" {
			t.Fatal("复制必须返回新任务 id")
		}
	})

	var firstCopyID string
	t.Run("重复请求返回既有任务且不新建", func(t *testing.T) {
		response := owner.Client.PostWithHeaders(URL("/tasks/"+taskID+"/copy"),
			map[string]any{}, map[string]string{"Idempotency-Key": key})
		// 重放不产生新对象，因此用 200 而不是 201 区分"确实创建了"与"重放"。
		response.RequireStatus(t, http.StatusOK)
		firstCopyID = Str(response.Data(t), "id")

		// 数据库中该项目的任务数应为 2（源任务 + 一份副本）。
		var count int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM tasks WHERE project_id = $1 AND deleted_at IS NULL`,
			projectID).Scan(&count); err != nil {
			t.Fatalf("统计任务失败: %v", err)
		}
		if count != 2 {
			t.Fatalf("任务数量 = %d，期望 2（幂等键必须阻止重复创建）", count)
		}
	})

	t.Run("幂等键已落库", func(t *testing.T) {
		var resultID string
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT result_id FROM idempotency_keys WHERE user_id = $1 AND idempotency_key = $2`,
			owner.UserID, key).Scan(&resultID); err != nil {
			t.Fatalf("幂等键必须持久化: %v", err)
		}
		if resultID != firstCopyID {
			t.Fatalf("幂等键记录的 result_id = %q，期望 %q", resultID, firstCopyID)
		}
	})

	t.Run("不同幂等键创建不同任务", func(t *testing.T) {
		response := owner.Client.PostWithHeaders(URL("/tasks/"+taskID+"/copy"),
			map[string]any{}, map[string]string{"Idempotency-Key": "copy-key-0002"})
		response.RequireStatus(t, http.StatusCreated)
	})

	t.Run("幂等键作用域包含操作与被复制任务", func(t *testing.T) {
		// 同一 key 用在另一个源任务上不应命中缓存。
		other := harness.CreateTask(t, owner, projectID, "另一个源任务", "2026-10-01", "2026-10-05")
		response := owner.Client.PostWithHeaders(URL("/tasks/"+other+"/copy"),
			map[string]any{}, map[string]string{"Idempotency-Key": key})
		response.RequireStatus(t, http.StatusCreated)
	})
}

// TestTaskHierarchyLimits 验证父子层级限制与区间覆盖。
func TestTaskHierarchyLimits(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	parent := harness.CreateTask(t, owner, projectID, "L1", "2026-10-01", "2026-10-31")

	createChild := func(parentID, name string) Response {
		t.Helper()
		return owner.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
			"name": name, "parent_id": parentID,
			"start_date": "2026-10-01", "end_date": "2026-10-05",
		})
	}

	var level2, level3 string
	t.Run("可以创建两层子任务", func(t *testing.T) {
		response := createChild(parent, "L2")
		response.RequireStatus(t, http.StatusCreated)
		level2 = Str(response.Data(t), "id")

		response = createChild(level2, "L3")
		response.RequireStatus(t, http.StatusCreated)
		level3 = Str(response.Data(t), "id")
	})

	t.Run("第四层被拒绝", func(t *testing.T) {
		// 需求文档 5.4：最大层级限制为 3 层。
		response := createChild(level3, "L4")
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireCode(t, "validation_error")
		response.RequireField(t, "parent_id")
	})

	t.Run("子任务日期必须落在父任务区间内", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
			"name": "越界子任务", "parent_id": parent,
			"start_date": "2026-09-01", "end_date": "2026-09-05",
		})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "start_date")
	})

	t.Run("父任务不能是自身或其子孙", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + parent)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+parent), map[string]any{
			"parent_id":           level3,
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "parent_id")
	})

	t.Run("父任务不能来自其他项目", func(t *testing.T) {
		otherProject := harness.CreateProject(t, owner, groupID, "另一个项目")
		otherTask := harness.CreateTask(t, owner, otherProject, "别的任务", "2026-10-01", "2026-10-31")

		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
			"name": "跨项目子任务", "parent_id": otherTask,
			"start_date": "2026-10-01", "end_date": "2026-10-05",
		})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireField(t, "parent_id")
	})

	t.Run("显式 null 可提升为顶层任务", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + level2)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+level2), map[string]any{
			"parent_id":           nil,
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusOK)
		if response.Data(t)["parent_id"] != nil {
			t.Fatalf("parent_id = %v，期望 null", response.Data(t)["parent_id"])
		}
	})
}

// TestTaskDatesEndpointOnlyAcceptsDates 验证甘特图日期端点字段收敛。
//
// Python 版本该端点接受全量任务字段，导致名称/状态/进度可被"仅日期"端点修改。
func TestTaskDatesEndpointOnlyAcceptsDates(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	taskID := harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	t.Run("可以正常修改日期", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+taskID+"/dates"), map[string]any{
			"start_date": "2026-10-02", "end_date": "2026-10-06",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusOK)

		data := response.Data(t)
		if Str(data, "start_date") != "2026-10-02" || Str(data, "end_date") != "2026-10-06" {
			t.Fatalf("日期未更新: start=%s end=%s", Str(data, "start_date"), Str(data, "end_date"))
		}
	})

	t.Run("不接受名称等非日期字段", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+taskID+"/dates"), map[string]any{
			"name":                "偷改名称",
			"start_date":          "2026-10-02",
			"end_date":            "2026-10-06",
			"expected_updated_at": Str(current, "updated_at"),
		})
		// 契约未定义该字段，必须显式拒绝而不是静默忽略。
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireCode(t, "validation_error")
	})

	t.Run("日期区间倒置被拒绝", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+taskID+"/dates"), map[string]any{
			"start_date": "2026-10-09", "end_date": "2026-10-01",
			"expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireCode(t, "invalid_date_range")
	})
}

// TestTaskMemberMustBeProjectMember 验证任务成员必须是项目成员。
//
// 需求文档 5.4：任务成员必须属于项目成员，持有者也必须是项目成员。
// 该不变式在数据库层没有约束，必须由用例层保证。
func TestTaskMemberMustBeProjectMember(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	outsider := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	taskID := harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	t.Run("非项目成员不能加入任务", func(t *testing.T) {
		response := owner.Client.Post(URL("/tasks/"+taskID+"/members"),
			map[string]any{"user_id": outsider.UserID})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireCode(t, "validation_error")
		response.RequireField(t, "user_id")
	})

	t.Run("成为项目成员后可以加入任务", func(t *testing.T) {
		owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": outsider.UserID}).RequireStatus(t, http.StatusCreated)

		owner.Client.Post(URL("/tasks/"+taskID+"/members"),
			map[string]any{"user_id": outsider.UserID}).RequireStatus(t, http.StatusCreated)
	})

	t.Run("不能移除唯一持有者", func(t *testing.T) {
		response := owner.Client.Delete(URL("/tasks/"+taskID+"/members/"+owner.UserID), nil)
		response.RequireStatus(t, http.StatusForbidden)
		response.RequireCode(t, "permission_denied")
	})

	// 需求文档 5.4 把"修改持有者"列为任务支持的编辑项；放在最后，
	// 因为它会把 owner 降级，前面以 owner 身份操作的子用例都必须先跑完。
	t.Run("任务持有者可以转移", func(t *testing.T) {
		transferred := owner.Client.Patch(URL("/tasks/"+taskID+"/members/"+outsider.UserID),
			map[string]any{"role": "owner"})
		transferred.RequireStatus(t, http.StatusOK)

		var owners int
		if err := harness.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM task_members WHERE task_id = $1 AND role = 'owner'`,
			taskID).Scan(&owners); err != nil {
			t.Fatalf("统计任务持有者失败: %v", err)
		}
		if owners != 1 {
			t.Fatalf("转移后任务持有者数量 = %d，期望恰好 1", owners)
		}

		// 转移后成员角色必须如实反映：新持有者是 owner，原持有者降为 member。
		//
		// 这里不assert"原持有者失去管理任务成员的能力"：任务成员变更只要求
		// **项目成员**（PermWrite），本来就不要求是任务持有者，因此转移不会改变这一点。
		members := owner.Client.Post(URL("/tasks/"+taskID+"/members/query"),
			map[string]any{"page": 1, "page_size": 50}).RequireStatus(t, http.StatusOK)
		roles := map[string]string{}
		for _, item := range members.ListData(t) {
			roles[Str(item, "user_id")] = Str(item, "role")
		}
		if roles[outsider.UserID] != "owner" {
			t.Errorf("接管者的角色 = %q，期望 owner", roles[outsider.UserID])
		}
		if roles[owner.UserID] != "member" {
			t.Errorf("原持有者的角色 = %q，期望 member", roles[owner.UserID])
		}

		// 已是持有者时再次请求转移：冲突，而不是"成功但没有变化"。
		repeat := outsider.Client.Patch(URL("/tasks/"+taskID+"/members/"+outsider.UserID),
			map[string]any{"role": "owner"})
		repeat.RequireStatus(t, http.StatusConflict)
		repeat.RequireField(t, "role")
	})
}

// TestTaskIsOverdueIsDerived 验证逾期是派生字段。
//
// harness 固定时钟为 2026-10-11，因此 2026-10-05 已逾期。
func TestTaskIsOverdueIsDerived(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	overdue := harness.CreateTask(t, owner, projectID, "逾期", "2026-10-01", "2026-10-05")
	future := harness.CreateTask(t, owner, projectID, "未到期", "2026-10-11", "2026-10-30")

	t.Run("未完成任务且已过期", func(t *testing.T) {
		response := owner.Client.Get(URL("/tasks/"+overdue)).RequireStatus(t, http.StatusOK)
		if response.Data(t)["is_overdue"] != true {
			t.Fatalf("is_overdue = %v，期望 true", response.Data(t)["is_overdue"])
		}
	})

	t.Run("未来到期不算逾期", func(t *testing.T) {
		response := owner.Client.Get(URL("/tasks/"+future)).RequireStatus(t, http.StatusOK)
		if response.Data(t)["is_overdue"] != false {
			t.Fatalf("is_overdue = %v，期望 false", response.Data(t)["is_overdue"])
		}
	})

	t.Run("已完成任务不算逾期", func(t *testing.T) {
		current := owner.Client.Get(URL("/tasks/" + overdue)).Data(t)
		owner.Client.Patch(URL("/tasks/"+overdue), map[string]any{
			"progress":            100,
			"expected_updated_at": Str(current, "updated_at"),
		}).RequireStatus(t, http.StatusOK)

		response := owner.Client.Get(URL("/tasks/"+overdue)).RequireStatus(t, http.StatusOK)
		if response.Data(t)["is_overdue"] != false {
			t.Fatalf("已完成任务的 is_overdue = %v，期望 false", response.Data(t)["is_overdue"])
		}
		// 状态应由进度推导为 completed。
		if got := Str(response.Data(t), "status"); got != "completed" {
			t.Fatalf("status = %q，期望 completed", got)
		}
	})

	t.Run("逾期不是持久化状态", func(t *testing.T) {
		// 直接改库验证：数据库没有 overdue 状态，CHECK 约束会拒绝写入。
		_, err := harness.Pool.Exec(context.Background(),
			`UPDATE tasks SET status = 'overdue' WHERE id = $1`, overdue)
		if err == nil {
			t.Fatal("数据库必须拒绝 overdue 状态（逾期是派生字段，不是持久化状态）")
		}
	})
}

// TestTaskVersionConflict 验证 PATCH 的 CAS 与必填版本。
func TestTaskVersionConflict(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	taskID := harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	updatedAt := Str(owner.Client.Get(URL("/tasks/"+taskID)).Data(t), "updated_at")

	t.Run("省略版本被拒绝", func(t *testing.T) {
		response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{"name": "改名"})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
	})

	t.Run("正确版本更新成功", func(t *testing.T) {
		response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"name": "新名称", "expected_updated_at": updatedAt,
		})
		response.RequireStatus(t, http.StatusOK)
		if Str(response.Data(t), "name") != "新名称" {
			t.Fatalf("name = %q，期望 新名称", Str(response.Data(t), "name"))
		}
	})

	t.Run("过期版本返回 409", func(t *testing.T) {
		response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"name": "再次改名", "expected_updated_at": updatedAt,
		})
		response.RequireStatus(t, http.StatusConflict)
		response.RequireCode(t, "version_conflict")
	})
}

// TestTaskProgressValidation 验证进度与优先级取值约束。
func TestTaskProgressValidation(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")
	taskID := harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	t.Run("进度越界被拒绝", func(t *testing.T) {
		for _, progress := range []int{-1, 101} {
			current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
			response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
				"progress": progress, "expected_updated_at": Str(current, "updated_at"),
			})
			response.RequireStatus(t, http.StatusUnprocessableEntity)
			response.RequireField(t, "progress")
		}
	})

	t.Run("非法优先级被拒绝", func(t *testing.T) {
		// priority 列没有 CHECK 约束，取值集合完全由领域层保证。
		current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
		response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
			"priority": 99, "expected_updated_at": Str(current, "updated_at"),
		})
		response.RequireStatus(t, http.StatusUnprocessableEntity)
		response.RequireField(t, "priority")
	})

	t.Run("合法优先级被接受", func(t *testing.T) {
		for priority := 0; priority <= 3; priority++ {
			current := owner.Client.Get(URL("/tasks/" + taskID)).Data(t)
			response := owner.Client.Patch(URL("/tasks/"+taskID), map[string]any{
				"priority": priority, "expected_updated_at": Str(current, "updated_at"),
			})
			response.RequireStatus(t, http.StatusOK)
			if got := response.Data(t)["priority"]; got != float64(priority) {
				t.Fatalf("priority = %v，期望 %d", got, priority)
			}
		}
	})
}

// TestTaskPaginationAndSorting 覆盖任务列表的分页与排序白名单。
func TestTaskPaginationAndSorting(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "迁移")

	for i := 0; i < 5; i++ {
		harness.CreateTask(t, owner, projectID,
			fmt.Sprintf("任务 %d", i), "2026-10-01", "2026-10-05")
	}

	t.Run("分页在 SQL 层生效且总数真实", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
			map[string]any{"page": 1, "page_size": 2})
		response.RequireStatus(t, http.StatusOK)

		if items := response.ListData(t); len(items) != 2 {
			t.Fatalf("第一页条目数 = %d，期望 2", len(items))
		}
		if total := response.Pagination(t)["total"]; total != float64(5) {
			t.Fatalf("total = %v，期望 5", total)
		}
	})

	t.Run("翻页不重不漏", func(t *testing.T) {
		seen := map[string]bool{}
		for page := 1; page <= 3; page++ {
			response := owner.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
				map[string]any{"page": page, "page_size": 2})
			response.RequireStatus(t, http.StatusOK)
			for _, item := range response.ListData(t) {
				id := Str(item, "id")
				if seen[id] {
					t.Fatalf("任务 %s 在翻页中重复出现", id)
				}
				seen[id] = true
			}
		}
		if len(seen) != 5 {
			t.Fatalf("翻页共取到 %d 个任务，期望 5", len(seen))
		}
	})

	t.Run("未知排序字段被拒绝", func(t *testing.T) {
		response := owner.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
			map[string]any{"sort": "secret_column"})
		response.RequireStatus(t, http.StatusBadRequest)
		response.RequireField(t, "sort")
	})
}

// TestTaskListRequiresProjectVisibility 验证非成员看不到项目下的任务。
func TestTaskListRequiresProjectVisibility(t *testing.T) {
	harness := NewHarness(t)
	owner := harness.RegisterAndLogin(t, "alice")
	outsider := harness.RegisterAndLogin(t, "bob")
	groupID := harness.CreateGroup(t, owner, "研发组")
	projectID := harness.CreateProject(t, owner, groupID, "私有项目")
	harness.CreateTask(t, owner, projectID, "任务", "2026-10-01", "2026-10-05")

	response := outsider.Client.Post(URL("/projects/"+projectID+"/tasks/query"), map[string]any{})
	response.RequireStatus(t, http.StatusNotFound)
	response.RequireCode(t, "project_not_found")

	// 未登录返回 401。
	harness.Client(t).Post(URL("/projects/"+projectID+"/tasks/query"), map[string]any{}).
		RequireStatus(t, http.StatusUnauthorized).
		RequireCode(t, "authentication_required")
}
