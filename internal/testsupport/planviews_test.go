package testsupport

import (
	"testing"
)

// 计划视图的集成测试（接口契约 4.7）。
//
// 这些用例的重点不是"接口能返回 200"，而是三件事：
//  1. 范围与共享规则（谁能进入个人视图、什么内容能进入组内视图）；
//  2. 逐对象权限收敛（组内访客只能看到已共享任务，且不能编辑）；
//  3. 派生字段与汇总统计**与返回的条目一致**（不允许统计与列表各算一套）。

// createProjectWithVisibility 在指定工作组下创建一个指定共享级别的项目。
func createProjectWithVisibility(
	t *testing.T, _ *Harness, actor Authenticated, groupID, name, visibility string,
) string {
	t.Helper()

	response := actor.Client.Post(URL("/projects"), map[string]any{
		"name":       name,
		"group_id":   groupID,
		"start_date": "2026-10-01",
		"end_date":   "2026-10-31",
		"visibility": visibility,
	})
	response.RequireStatus(t, 201)
	return Str(response.Data(t), "id")
}

// shareTask 把任务设为"已共享"，使其可以进入组内视图。
func shareTask(t *testing.T, _ *Harness, actor Authenticated, taskID string) {
	t.Helper()

	current := actor.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200)
	actor.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"is_shared":           true,
		"expected_updated_at": Str(current.Data(t), "updated_at"),
	}).RequireStatus(t, 200)
}

// planViewProjects 取出计划视图响应中的项目 id 集合。
func planViewProjects(t *testing.T, response Response) map[string]bool {
	t.Helper()

	payload := response.Data(t)
	raw, ok := payload["projects"].([]any)
	if !ok {
		t.Fatalf("响应缺少 projects 数组: %s", string(response.Body))
	}
	ids := map[string]bool{}
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("projects 元素不是对象: %s", string(response.Body))
		}
		ids[Str(object, "id")] = true
	}
	return ids
}

// planViewTasks 取出计划视图响应中的任务 id 集合。
func planViewTasks(t *testing.T, response Response) map[string]bool {
	t.Helper()

	payload := response.Data(t)
	raw, ok := payload["tasks"].([]any)
	if !ok {
		t.Fatalf("响应缺少 tasks 数组: %s", string(response.Body))
	}
	ids := map[string]bool{}
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tasks 元素不是对象: %s", string(response.Body))
		}
		ids[Str(object, "id")] = true
	}
	return ids
}

// planViewTask 按 id 找到任务对象。
func planViewTask(t *testing.T, response Response, taskID string) map[string]any {
	t.Helper()

	raw, _ := response.Data(t)["tasks"].([]any)
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if ok && Str(object, "id") == taskID {
			return object
		}
	}
	t.Fatalf("任务 %s 不在计划视图中: %s", taskID, string(response.Body))
	return nil
}

// TestPersonalPlanViewScopeIsMembership 覆盖个人视图的范围定义。
//
// 范围是"我是项目成员的项目"，**不**额外按 created_by 放宽：
// 否则会出现"把项目移交出去并被移出成员后仍能看到它"的越权读取。
func TestPersonalPlanViewScopeIsMembership(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "pv_alice")
	bob := harness.RegisterAndLogin(t, "pv_bob")

	aliceGroup := harness.CreateGroup(t, alice, "个人视图_A组")
	bobGroup := harness.CreateGroup(t, bob, "个人视图_B组")

	aliceProject := harness.CreateProject(t, alice, aliceGroup, "alice 的项目")
	bobProject := harness.CreateProject(t, bob, bobGroup, "bob 的项目")
	aliceTask := harness.CreateTask(t, alice, aliceProject, "alice 的任务", "2026-10-01", "2026-10-05")
	harness.CreateTask(t, bob, bobProject, "bob 的任务", "2026-10-01", "2026-10-05")

	view := alice.Client.Get(URL("/plan-views/personal")).RequireStatus(t, 200)
	projects := planViewProjects(t, view)
	if !projects[aliceProject] {
		t.Error("个人视图必须包含自己参与的项目")
	}
	if projects[bobProject] {
		t.Error("个人视图不得包含别人的项目")
	}
	if !planViewTasks(t, view)[aliceTask] {
		t.Error("个人视图必须包含自己项目下的任务")
	}

	// 把 bob 加为项目成员后，bob 的个人视图应当出现该项目。
	alice.Client.Post(URL("/projects/"+aliceProject+"/members"),
		map[string]any{"user_id": bob.UserID}).RequireStatus(t, 201)

	bobView := bob.Client.Get(URL("/plan-views/personal")).RequireStatus(t, 200)
	if !planViewProjects(t, bobView)[aliceProject] {
		t.Error("被加入项目后，该项目应出现在 bob 的个人视图里")
	}
	if !planViewTasks(t, bobView)[aliceTask] {
		t.Error("被加入项目后，该项目的任务应出现在 bob 的个人视图里")
	}

	// 被加入项目后出现，被移出后立即消失。
	//
	// 这里刻意不测"创建者被移出后仍能看到吗"：**项目持有者无法转移**，
	// 唯一 owner 也不能被移除（projects 模块的成员变更规则 + 部分唯一索引），
	// 因此"created_by 是我但我不再是成员"这一状态目前**无法通过接口到达**。
	// 范围 SQL 仍然只按成员关系判定，这样即使将来引入持有者转移也不会越权。
	alice.Client.Delete(URL("/projects/"+aliceProject+"/members/"+bob.UserID), nil).
		RequireStatus(t, 204)

	afterRemoval := bob.Client.Get(URL("/plan-views/personal")).RequireStatus(t, 200)
	if planViewProjects(t, afterRemoval)[aliceProject] {
		t.Error("被移出项目后，个人视图不得再包含它")
	}
	if planViewTasks(t, afterRemoval)[aliceTask] {
		t.Error("被移出项目后，个人视图不得再包含它的任务")
	}

	// 创建者本人（同时是成员与持有者）始终可见。
	if !planViewProjects(t, alice.Client.Get(URL("/plan-views/personal")).
		RequireStatus(t, 200))[aliceProject] {
		t.Error("创建者应当能看到自己的项目")
	}
}

// TestGroupPlanViewOnlyExposesSharedContent 覆盖组内视图的共享规则。
func TestGroupPlanViewOnlyExposesSharedContent(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_owner")
	visitor := harness.RegisterAndLogin(t, "pv_visitor")
	outsider := harness.RegisterAndLogin(t, "pv_outsider")

	groupID := harness.CreateGroup(t, owner, "组内视图工作组")
	harness.AddGroupMember(t, owner, groupID, visitor.UserID)

	privateProject := harness.CreateProject(t, owner, groupID, "私有项目")
	sharedProject := createProjectWithVisibility(t, harness, owner, groupID, "组内只读项目", "group_readonly")
	editableProject := createProjectWithVisibility(t, harness, owner, groupID, "组内可编辑项目", "group_editable")

	privateTask := harness.CreateTask(t, owner, privateProject, "私有项目任务", "2026-10-01", "2026-10-05")

	sharedTask := harness.CreateTask(t, owner, sharedProject, "已共享任务", "2026-10-01", "2026-10-05")
	shareTask(t, harness, owner, sharedTask)

	unsharedTask := harness.CreateTask(t, owner, sharedProject, "未共享任务", "2026-10-01", "2026-10-05")

	editableTask := harness.CreateTask(t, owner, editableProject, "可编辑共享任务", "2026-10-01", "2026-10-05")
	shareTask(t, harness, owner, editableTask)

	view := visitor.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 200)
	projects := planViewProjects(t, view)
	if projects[privateProject] {
		t.Error("未共享的私有项目不得进入组内视图（需求文档 4.3）")
	}
	if !projects[sharedProject] || !projects[editableProject] {
		t.Error("组内只读与组内可编辑项目都必须进入组内视图")
	}

	tasks := planViewTasks(t, view)
	if tasks[privateTask] {
		t.Error("私有项目的任务不得进入组内视图")
	}
	if tasks[unsharedTask] {
		t.Error("项目已共享但任务未共享时，任务不得出现在组内视图（需求文档 4.3）")
	}
	if !tasks[sharedTask] || !tasks[editableTask] {
		t.Error("已共享任务必须出现在组内视图")
	}

	// 逐任务的可编辑性来自 domain.TaskPermission：
	// 组内只读 → 不可编辑；组内可编辑 → 可编辑。
	if got := planViewTask(t, view, sharedTask)["can_edit"]; got != false {
		t.Errorf("组内只读项目的任务 can_edit = %v，期望 false", got)
	}
	if got := planViewTask(t, view, editableTask)["can_edit"]; got != true {
		t.Errorf("组内可编辑项目的任务 can_edit = %v，期望 true", got)
	}

	// 关键补充：**项目成员**在组内视图里同样只能看到已共享任务。
	//
	// 需求文档 4.3 的规则是"组内页面只展示已共享的任务"，与查看者是不是项目成员无关。
	// 少了这条断言，"组内视图不再要求任务已共享"这类改动不会被发现：
	// 非成员访客仍会被逐任务权限收敛挡住，看起来一切正常。
	ownerView := owner.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 200)
	if planViewTasks(t, ownerView)[unsharedTask] {
		t.Error("即使查看者是项目成员，组内视图也不得包含未共享任务（需求文档 4.3）")
	}
	if !planViewTasks(t, ownerView)[sharedTask] {
		t.Error("组内视图应包含已共享任务")
	}

	// 非本组成员：404，而不是 403（不泄漏组是否存在）。
	outsider.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 404).
		RequireCode(t, "group_not_found")

	// 未登录：会话网关拦下。
	harness.Client(t).Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 401)

	// 被移出工作组后立即失去访问。
	owner.Client.Delete(URL("/groups/"+groupID+"/members/"+visitor.UserID), nil).RequireStatus(t, 204)
	visitor.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 404)
}

// TestGroupPlanViewArchivedHandling 覆盖归档项目的默认排除与显式包含。
func TestGroupPlanViewArchivedHandling(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_arch_owner")
	groupID := harness.CreateGroup(t, owner, "归档视图工作组")

	active := createProjectWithVisibility(t, harness, owner, groupID, "活动项目", "group_readonly")
	archived := createProjectWithVisibility(t, harness, owner, groupID, "将被归档的项目", "group_readonly")

	activeTask := harness.CreateTask(t, owner, active, "活动任务", "2026-10-01", "2026-10-05")
	shareTask(t, harness, owner, activeTask)

	archivedTask := harness.CreateTask(t, owner, archived, "归档任务", "2026-10-01", "2026-10-05")
	shareTask(t, harness, owner, archivedTask)

	// 归档只是把项目设为只读并默认从活动列表排除，不删除数据。
	archivedVersion := owner.Client.Get(URL("/projects/"+archived)).RequireStatus(t, 200)
	owner.Client.Post(URL("/projects/"+archived+"/archive"), map[string]any{
		"expected_updated_at": Str(archivedVersion.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	// 默认：归档项目及其任务都不出现。
	defaultView := owner.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 200)
	if planViewProjects(t, defaultView)[archived] {
		t.Error("归档项目默认不得出现在活动视图中（需求文档 5.3）")
	}
	if planViewTasks(t, defaultView)[archivedTask] {
		t.Error("归档项目下的任务默认不得出现在活动视图中")
	}
	if !planViewProjects(t, defaultView)[active] {
		t.Error("活动项目必须出现")
	}

	// include_archived=true：归档项目与其任务都可见（历史数据可查询），
	// 且两者必须**同时**出现——不能出现"项目在、任务不在"的不一致。
	included := owner.Client.Get(URL("/plan-views/groups/"+groupID+"?include_archived=true")).
		RequireStatus(t, 200)
	if !planViewProjects(t, included)[archived] {
		t.Error("include_archived=true 时必须能看到归档项目")
	}
	if !planViewTasks(t, included)[archivedTask] {
		t.Error("include_archived=true 时归档项目下的任务也必须可见")
	}

	// 归档项目在视图中标记为只读，且其任务不可编辑。
	raw, _ := included.Data(t)["projects"].([]any)
	for _, item := range raw {
		object := item.(map[string]any)
		if Str(object, "id") != archived {
			continue
		}
		if got := object["is_read_only"]; got != true {
			t.Errorf("归档项目的 is_read_only = %v，期望 true", got)
		}
		if got := object["is_archived"]; got != true {
			t.Errorf("归档项目的 is_archived = %v，期望 true", got)
		}
	}
	if got := planViewTask(t, included, archivedTask)["can_edit"]; got != false {
		t.Errorf("归档项目下的任务 can_edit = %v，期望 false（归档项目只读）", got)
	}
}

// TestGanttViewExposesHierarchyMembersAndCanEdit 覆盖甘特图端点的内容完整性。
func TestGanttViewExposesHierarchyMembersAndCanEdit(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_gantt_owner")
	member := harness.RegisterAndLogin(t, "pv_gantt_member")
	groupID := harness.CreateGroup(t, owner, "甘特图工作组")
	harness.AddGroupMember(t, owner, groupID, member.UserID)

	projectID := harness.CreateProject(t, owner, groupID, "甘特图项目")
	owner.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)

	parent := harness.CreateTask(t, owner, projectID, "父任务", "2026-10-01", "2026-10-31")

	child := owner.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
		"name":         "子任务",
		"start_date":   "2026-10-05",
		"end_date":     "2026-10-20",
		"parent_id":    parent,
		"is_milestone": true,
		"priority":     2,
	}).RequireStatus(t, 201)
	childID := Str(child.Data(t), "id")

	// 给子任务加一名成员，用来验证 members 数组与 owner 字段。
	owner.Client.Post(URL("/tasks/"+childID+"/members"),
		map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)

	// owner 改一次子任务：验证"最后修改人"。
	childVersion := owner.Client.Get(URL("/tasks/"+childID)).RequireStatus(t, 200)
	member.Client.Patch(URL("/tasks/"+childID), map[string]any{
		"progress":            50,
		"expected_updated_at": Str(childVersion.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	view := member.Client.Get(URL("/plan-views/projects/"+projectID+"/gantt")).
		RequireStatus(t, 200)
	payload := view.Data(t)

	project, ok := payload["project"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 project 对象: %s", string(view.Body))
	}
	if Str(project, "id") != projectID {
		t.Errorf("project.id = %q，期望 %q", Str(project, "id"), projectID)
	}
	if got := project["my_role"]; got != "member" {
		t.Errorf("project.my_role = %v，期望 member", got)
	}
	if got := payload["can_edit_tasks"]; got != true {
		t.Errorf("can_edit_tasks = %v，期望 true（项目成员）", got)
	}
	if got := payload["can_manage_project"]; got != false {
		t.Errorf("can_manage_project = %v，期望 false（非持有者不能改项目本身）", got)
	}

	tasks, ok := payload["tasks"].([]any)
	if !ok || len(tasks) != 2 {
		t.Fatalf("tasks 条目数 = %d，期望 2: %s", len(tasks), string(view.Body))
	}

	childObject := planViewTask(t, view, childID)
	if Str(childObject, "parent_id") != parent {
		t.Errorf("子任务的 parent_id = %q，期望 %q", Str(childObject, "parent_id"), parent)
	}
	if got := childObject["is_milestone"]; got != true {
		t.Errorf("is_milestone = %v，期望 true", got)
	}
	if got := childObject["progress"]; got != float64(50) {
		t.Errorf("progress = %v，期望 50", got)
	}
	if got := Str(childObject, "updated_by"); got != member.UserID {
		t.Errorf("updated_by = %q，期望最后修改人 %q", got, member.UserID)
	}
	if got := Str(childObject, "updated_by_name"); got != member.Username {
		t.Errorf("updated_by_name = %q，期望 %q（需求文档 5.6：最后修改人）", got, member.Username)
	}

	ownerObject, _ := childObject["owner"].(map[string]any)
	if ownerObject == nil || Str(ownerObject, "user_id") != owner.UserID {
		t.Errorf("owner = %v，期望持有者 %q", childObject["owner"], owner.UserID)
	}
	if got := len(childObject["members"].([]any)); got != 2 {
		t.Errorf("members 条目数 = %d，期望 2", got)
	}
	if got := childObject["can_edit"]; got != true {
		t.Errorf("项目成员对任务 can_edit = %v，期望 true", got)
	}
}

// TestGanttViewHidesUnsharedTasksFromGroupVisitors 覆盖甘特图的逐任务权限收敛。
func TestGanttViewHidesUnsharedTasksFromGroupVisitors(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_gantt_guard")
	visitor := harness.RegisterAndLogin(t, "pv_gantt_visitor")
	groupID := harness.CreateGroup(t, owner, "甘特图收敛工作组")
	harness.AddGroupMember(t, owner, groupID, visitor.UserID)

	projectID := createProjectWithVisibility(t, harness, owner, groupID, "只读共享项目", "group_readonly")

	sharedTask := harness.CreateTask(t, owner, projectID, "已共享任务", "2026-10-01", "2026-10-05")
	shareTask(t, harness, owner, sharedTask)
	unsharedTask := harness.CreateTask(t, owner, projectID, "未共享任务", "2026-10-01", "2026-10-05")

	// 持有者看到全部任务。
	ownerView := owner.Client.Get(URL("/plan-views/projects/"+projectID+"/gantt")).
		RequireStatus(t, 200)
	if got := len(planViewTasks(t, ownerView)); got != 2 {
		t.Errorf("持有者看到 %d 个任务，期望 2", got)
	}

	// 组内访客（非项目成员）只能看到已共享任务，且不可编辑。
	visitorView := visitor.Client.Get(URL("/plan-views/projects/"+projectID+"/gantt")).
		RequireStatus(t, 200)
	if planViewTasks(t, visitorView)[unsharedTask] {
		t.Error("组内访客不得看到未共享任务")
	}
	if !planViewTasks(t, visitorView)[sharedTask] {
		t.Error("组内访客必须看到已共享任务")
	}
	if got := planViewTask(t, visitorView, sharedTask)["can_edit"]; got != false {
		t.Errorf("组内只读访客的 can_edit = %v，期望 false", got)
	}
	if got := visitorView.Data(t)["can_edit_tasks"]; got != false {
		t.Errorf("组内只读访客的 can_edit_tasks = %v，期望 false", got)
	}
}

// TestGanttViewRejectsInvisibleProject 覆盖"不可见即不存在"。
func TestGanttViewRejectsInvisibleProject(t *testing.T) {
	harness := NewHarness(t)

	alice := harness.RegisterAndLogin(t, "pv_hidden_alice")
	bob := harness.RegisterAndLogin(t, "pv_hidden_bob")

	aliceGroup := harness.CreateGroup(t, alice, "隐藏项目_A组")
	harness.CreateGroup(t, bob, "隐藏项目_B组")
	privateProject := harness.CreateProject(t, alice, aliceGroup, "alice 的私有项目")

	// bob 既不是项目成员，也不在 alice 的组里：必须 404（不是 403）。
	bob.Client.Get(URL("/plan-views/projects/"+privateProject+"/gantt")).
		RequireStatus(t, 404).RequireCode(t, "project_not_found")

	// 合法但不存在：同样是 project_not_found。
	bob.Client.Get(URL("/plan-views/projects/00000000-0000-7000-8000-000000000000/gantt")).
		RequireStatus(t, 404).RequireCode(t, "project_not_found")

	// 非法 UUID：400，不能交给数据库退化成 500。
	bob.Client.Get(URL("/plan-views/projects/not-a-uuid/gantt")).
		RequireStatus(t, 400).RequireCode(t, "invalid_request")

	// bob 自己的项目组里能看到 alice 的组？不：bobGroup 与 aliceGroup 无关，
	// 因此 bob 也看不到 alice 的组内视图。
	bob.Client.Get(URL("/plan-views/groups/"+aliceGroup)).RequireStatus(t, 404)
}

// TestPlanViewFiltersAndValidation 覆盖筛选与参数校验。
func TestPlanViewFiltersAndValidation(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_filter_owner")
	member := harness.RegisterAndLogin(t, "pv_filter_member")
	groupID := harness.CreateGroup(t, owner, "筛选工作组")
	harness.AddGroupMember(t, owner, groupID, member.UserID)

	projectID := createProjectWithVisibility(t, harness, owner, groupID, "筛选项目", "group_readonly")
	otherProject := createProjectWithVisibility(t, harness, owner, groupID, "另一个筛选项目", "group_readonly")

	owner.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)

	// 任务 A：owner 是任务持有者，member 是普通成员；未开始且已逾期。
	// 任务持有者无法转移（唯一 owner 不能降级、也不能再提升第二个），
	// 因此这里只用"持有者 = 创建者、成员 = 另一个人"这一可到达的组合来验证筛选。
	taskA := harness.CreateTask(t, owner, projectID, "任务A", "2026-10-01", "2026-10-05")
	owner.Client.Post(URL("/tasks/"+taskA+"/members"),
		map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)
	shareTask(t, harness, owner, taskA)
	// 任务 B：未开始，未逾期（结束日期在未来），优先级 3。
	taskB := owner.Client.Post(URL("/projects/"+projectID+"/tasks"), map[string]any{
		"name":       "任务B",
		"start_date": "2026-10-20",
		"end_date":   "2026-10-25",
		"priority":   3,
	}).RequireStatus(t, 201)
	taskBID := Str(taskB.Data(t), "id")
	shareTask(t, harness, owner, taskBID)

	// 任务 C：另一个项目的已共享任务，用于验证 project_id 筛选。
	taskC := harness.CreateTask(t, owner, otherProject, "任务C", "2026-10-01", "2026-10-30")
	shareTask(t, harness, owner, taskC)

	base := URL("/plan-views/groups/" + groupID)

	// 组合筛选：owner 与 member 必须分别判定（历史缺陷：共用 JOIN 导致恒为空）。
	// owner 是任务 A 的持有者，member 是任务 A 的成员，因此这个组合恰好命中 A。
	combined := owner.Client.Get(base+"?task_owner_id="+owner.UserID+
		"&task_member_id="+member.UserID).RequireStatus(t, 200)
	if tasks := planViewTasks(t, combined); !tasks[taskA] || len(tasks) != 1 {
		t.Errorf("task_owner_id + task_member_id 组合筛选应命中任务A，实际 %v", tasks)
	}

	// 两个条件指向不同的人：必须为空（说明没有共用 JOIN）。
	empty := owner.Client.Get(base+"?task_owner_id="+member.UserID+
		"&task_member_id="+owner.UserID).RequireStatus(t, 200)
	if tasks := planViewTasks(t, empty); len(tasks) != 0 {
		t.Errorf("持有者与成员指向不同的人时不应命中任何任务，实际 %v", tasks)
	}

	// overdue=true 只返回逾期任务。
	overdue := owner.Client.Get(base+"?overdue=true").RequireStatus(t, 200)
	if tasks := planViewTasks(t, overdue); !tasks[taskA] || tasks[taskBID] {
		t.Errorf("overdue=true 应只命中逾期任务，实际 %v", tasks)
	}

	// 时间范围按结束日期筛选。
	window := owner.Client.Get(base+"?due_from=2026-10-20&due_to=2026-10-26").RequireStatus(t, 200)
	if tasks := planViewTasks(t, window); !tasks[taskBID] || tasks[taskA] {
		t.Errorf("due_from/due_to 应只命中结束日期在区间内的任务，实际 %v", tasks)
	}

	// 优先级筛选。
	priority := owner.Client.Get(base+"?priority=3").RequireStatus(t, 200)
	if tasks := planViewTasks(t, priority); !tasks[taskBID] || tasks[taskA] {
		t.Errorf("priority=3 应只命中任务B，实际 %v", tasks)
	}

	// project_id 同时作用于项目与任务两侧。
	scoped := owner.Client.Get(base+"?project_id="+projectID).RequireStatus(t, 200)
	if projects := planViewProjects(t, scoped); len(projects) != 1 || !projects[projectID] {
		t.Errorf("project_id 筛选后的项目集合 = %v，期望只含目标项目", projects)
	}
	if tasks := planViewTasks(t, scoped); tasks[taskC] {
		t.Error("project_id 筛选必须同时作用于任务")
	}

	// 任务状态筛选。
	inProgress := owner.Client.Get(base+"?task_status=in_progress").RequireStatus(t, 200)
	if tasks := planViewTasks(t, inProgress); len(tasks) != 0 {
		t.Errorf("task_status=in_progress 不应命中未开始的任务，实际 %v", tasks)
	}

	// --- 参数校验 ---
	rejected := []struct {
		query  string
		field  string
		reason string
	}{
		{"?status=completed", "status", "契约未定义的查询参数"},
		{"?task_statuss=completed", "task_statuss", "拼错字段名"},
		{"?task_status=bogus", "task_status", "非法状态取值"},
		{"?priority=9", "priority", "优先级越界"},
		{"?priority=abc", "priority", "优先级非整数"},
		{"?project_id=not-a-uuid", "project_id", "非法 UUID"},
		{"?due_from=2026/10/01", "due_from", "非法日期格式"},
		{"?overdue=maybe", "overdue", "非法布尔值"},
	}
	for _, tc := range rejected {
		response := owner.Client.Get(base + tc.query)
		response.RequireStatus(t, 400).RequireCode(t, "invalid_request")
		response.RequireField(t, tc.field)
	}

	// 时间范围倒置：契约规定 400 invalid_date_range，而不是笼统的 400。
	inverted := owner.Client.Get(base + "?due_from=2026-10-31&due_to=2026-10-01")
	inverted.RequireStatus(t, 400).RequireCode(t, "invalid_date_range").
		RequireField(t, "due_from").RequireField(t, "due_to")

	// 甘特图不接受任何筛选参数：任何筛选都会让画面不完整。
	gantt := owner.Client.Get(URL("/plan-views/projects/" + projectID + "/gantt?task_status=completed"))
	gantt.RequireStatus(t, 400).RequireCode(t, "invalid_request")

	// 个人视图支持同一套筛选。
	personal := owner.Client.Get(URL("/plan-views/personal?overdue=true")).RequireStatus(t, 200)
	if tasks := planViewTasks(t, personal); !tasks[taskA] || tasks[taskBID] {
		t.Errorf("个人视图的 overdue=true 应只命中逾期任务，实际 %v", tasks)
	}
}

// TestPlanViewSummaryMatchesReturnedItems 锁住"统计与列表同源"。
//
// 统计与列表由**同一个切片**算出，因此二者不一致在结构上就不可能发生；
// 本用例把这一点显式断言下来，防止日后有人为了"性能"改成两条独立的 SQL。
func TestPlanViewSummaryMatchesReturnedItems(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_summary_owner")
	groupID := harness.CreateGroup(t, owner, "汇总工作组")

	projectA := createProjectWithVisibility(t, harness, owner, groupID, "汇总项目A", "group_readonly")
	projectB := createProjectWithVisibility(t, harness, owner, groupID, "汇总项目B", "group_readonly")

	// 三个任务：未开始/逾期、进行中、已完成；进度 0/50/100。
	overdueTask := harness.CreateTask(t, owner, projectA, "逾期任务", "2026-10-01", "2026-10-05")
	shareTask(t, harness, owner, overdueTask)

	inProgress := harness.CreateTask(t, owner, projectA, "进行中任务", "2026-10-01", "2026-10-20")
	shareTask(t, harness, owner, inProgress)
	version := owner.Client.Get(URL("/tasks/"+inProgress)).RequireStatus(t, 200)
	owner.Client.Patch(URL("/tasks/"+inProgress), map[string]any{
		"progress": 50, "status": "in_progress",
		"expected_updated_at": Str(version.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	completed := harness.CreateTask(t, owner, projectB, "已完成任务", "2026-10-01", "2026-10-10")
	shareTask(t, harness, owner, completed)
	version = owner.Client.Get(URL("/tasks/"+completed)).RequireStatus(t, 200)
	owner.Client.Patch(URL("/tasks/"+completed), map[string]any{
		"progress": 100, "status": "completed",
		"expected_updated_at": Str(version.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	view := owner.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 200)
	summary, ok := view.Data(t)["summary"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 summary: %s", string(view.Body))
	}

	projects := planViewProjects(t, view)
	tasks := planViewTasks(t, view)

	if got := int(summary["project_count"].(float64)); got != len(projects) {
		t.Errorf("summary.project_count = %d，实际项目数 = %d", got, len(projects))
	}
	if got := int(summary["task_count"].(float64)); got != len(tasks) {
		t.Errorf("summary.task_count = %d，实际任务数 = %d", got, len(tasks))
	}
	if got := int(summary["in_progress_task_count"].(float64)); got != 1 {
		t.Errorf("summary.in_progress_task_count = %d，期望 1", got)
	}
	if got := int(summary["overdue_task_count"].(float64)); got != 1 {
		t.Errorf("summary.overdue_task_count = %d，期望 1", got)
	}
	// 整体完成率是有效任务进度的简单平均值：(0 + 50 + 100) / 3 ≈ 50。
	if got := int(summary["overall_progress"].(float64)); got != 50 {
		t.Errorf("summary.overall_progress = %d，期望 50", got)
	}

	// 项目自身的派生字段同样与"它在该视图内的任务"一致。
	raw, _ := view.Data(t)["projects"].([]any)
	for _, item := range raw {
		object := item.(map[string]any)
		if Str(object, "id") != projectB {
			continue
		}
		if got := int(object["task_count"].(float64)); got != 1 {
			t.Errorf("项目B的 task_count = %d，期望 1", got)
		}
		if got := int(object["progress"].(float64)); got != 100 {
			t.Errorf("项目B的 progress = %d，期望 100", got)
		}
	}

	// 取消的任务不参与进度平均（需求文档 5.3）。
	cancelled := harness.CreateTask(t, owner, projectB, "取消的任务", "2026-10-01", "2026-10-10")
	shareTask(t, harness, owner, cancelled)
	version = owner.Client.Get(URL("/tasks/"+cancelled)).RequireStatus(t, 200)
	owner.Client.Patch(URL("/tasks/"+cancelled), map[string]any{
		"progress": 0, "status": "cancelled",
		"expected_updated_at": Str(version.Data(t), "updated_at"),
	}).RequireStatus(t, 200)

	afterCancel := owner.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 200)
	afterSummary := afterCancel.Data(t)["summary"].(map[string]any)
	// (0 + 50 + 100 + 0) / 4 会因为取消任务而被拉低到 37；正确结果是排除它仍为 50。
	if got := int(afterSummary["overall_progress"].(float64)); got != 50 {
		t.Errorf("加入已取消任务后 overall_progress = %d，期望仍为 50（已取消不参与计算）", got)
	}
	if got := int(afterSummary["task_count"].(float64)); got != 4 {
		t.Errorf("task_count = %d，期望 4（已取消任务仍出现在列表里，只是不参与进度）", got)
	}
}

// TestPersonalPlanViewExcludesLogicallyDeletedTasks 验证逻辑删除的任务不进入计划视图。
//
// 计划视图是"计划"：已删除任务不属于任何计划。历史数据仍可通过
// POST /projects/{id}/tasks/query?include_deleted=true 查询。
func TestPersonalPlanViewExcludesLogicallyDeletedTasks(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "pv_deleted_owner")
	groupID := harness.CreateGroup(t, owner, "逻辑删除工作组")
	projectID := harness.CreateProject(t, owner, groupID, "逻辑删除项目")

	taskID := harness.CreateTask(t, owner, projectID, "将被删除的任务", "2026-10-01", "2026-10-05")

	before := owner.Client.Get(URL("/plan-views/personal")).RequireStatus(t, 200)
	if !planViewTasks(t, before)[taskID] {
		t.Fatal("删除前任务应在个人计划视图中")
	}

	version := owner.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200)
	owner.Client.Delete(URL("/tasks/"+taskID), map[string]any{
		"expected_updated_at": Str(version.Data(t), "updated_at"),
	}).RequireStatus(t, 204)

	after := owner.Client.Get(URL("/plan-views/personal")).RequireStatus(t, 200)
	if planViewTasks(t, after)[taskID] {
		t.Error("逻辑删除的任务不得出现在计划视图中")
	}
}
