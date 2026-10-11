package testsupport

import (
	"context"
	"testing"
)

// 系统管理员放行与工作组所有权转移（docs/04 4.4/4.5/4.6，docs/01 权限基线）。
//
// 背景：需求文档 4.1 规定系统管理员"管理所有用户、工作组和系统级数据，处理异常数据"。
// 早期实现里这条只在少数几处生效（用户管理、修改工作组），其余一律 404——于是
// "持有者已离职/被停用"这类异常没有任何受支持的补救路径。
//
// 这一组用例同时锁住放行的**边界**：管理员豁免的是"权限判定"，
// 不豁免状态规则与不变式（归档只读、唯一持有者不可降级、成员一致性）。

// promoteToAdmin 已在本包 users_test.go 中定义（直接写库造前置状态）。
// 注意：生产环境的受支持路径是配置驱动的引导管理员（docs/03 3.6.1）；
// 这里保留 SQL 方式只是为了在用例里临时造出"另一个管理员"。

// TestAdminCanReachDataWithoutMembership 覆盖管理员对三域数据的读与管。
func TestAdminCanReachDataWithoutMembership(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "adm_owner")
	admin := harness.RegisterAndLogin(t, "adm_admin")
	promoteToAdmin(t, harness, admin.UserID)

	groupID := harness.CreateGroup(t, owner, "管理员可达工作组")
	projectID := harness.CreateProject(t, owner, groupID, "管理员可达项目")
	taskID := harness.CreateTask(t, owner, projectID, "管理员可达任务", "2026-10-01", "2026-10-05")

	// 管理员不是组员、不是项目成员、也不是任务成员。
	admin.Client.Get(URL("/groups/"+groupID)).RequireStatus(t, 200)
	admin.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200)
	admin.Client.Get(URL("/tasks/"+taskID)).RequireStatus(t, 200)

	// 列表也不应把管理员挡在外面：否则他连"要处理哪个对象"都找不到。
	groups := admin.Client.Post(URL("/groups/query"),
		map[string]any{"page": 1, "page_size": 100}).RequireStatus(t, 200)
	if !containsRecord(groups.ListData(t), groupID) {
		t.Error("管理员的工作组列表应包含自己不是成员的组")
	}

	projects := admin.Client.Post(URL("/projects/query"),
		map[string]any{"page": 1, "page_size": 100}).RequireStatus(t, 200)
	if !containsRecord(projects.ListData(t), projectID) {
		t.Error("管理员的项目列表应包含自己不是成员的项目")
	}

	tasks := admin.Client.Post(URL("/projects/"+projectID+"/tasks/query"),
		map[string]any{"page": 1, "page_size": 100}).RequireStatus(t, 200)
	if !containsRecord(tasks.ListData(t), taskID) {
		t.Error("管理员的任务列表应包含自己不是成员的项目下的任务")
	}

	// 管理能力：改项目、改任务、管理成员。
	projectVersion := Str(admin.Client.Get(URL("/projects/"+projectID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	admin.Client.Patch(URL("/projects/"+projectID), map[string]any{
		"name":                "管理员改过的项目",
		"expected_updated_at": projectVersion,
	}).RequireStatus(t, 200)

	third := harness.RegisterAndLogin(t, "adm_third")
	admin.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": third.UserID}).RequireStatus(t, 201)

	taskVersion := Str(admin.Client.Get(URL("/tasks/"+taskID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	admin.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"name":                "管理员改过的任务",
		"expected_updated_at": taskVersion,
	}).RequireStatus(t, 200)

	// 组管理：改名、管理成员。
	groupVersion := Str(admin.Client.Get(URL("/groups/"+groupID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	admin.Client.Patch(URL("/groups/"+groupID), map[string]any{
		"name":                "管理员改过的组",
		"expected_updated_at": groupVersion,
	}).RequireStatus(t, 200)
	admin.Client.Post(URL("/groups/"+groupID+"/members"),
		map[string]any{"user_id": third.UserID}).RequireStatus(t, 201)

	// 只读聚合视图同样放行：管理员需要看任意组的共享计划来定位异常。
	admin.Client.Get(URL("/plan-views/groups/"+groupID)).RequireStatus(t, 200)
	admin.Client.Get(URL("/plan-views/projects/"+projectID+"/gantt")).RequireStatus(t, 200)
}

// TestAdminBypassDoesNotBreakInvariants 锁住放行的边界。
//
// "管理员放行"只针对**权限判定**；状态规则与不变式对管理员一样生效。
// 这条边界必须显式测，否则"管理员可以绕过一切"会成为一个没有护栏的假设。
func TestAdminBypassDoesNotBreakInvariants(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "bound_owner")
	admin := harness.RegisterAndLogin(t, "bound_admin")
	promoteToAdmin(t, harness, admin.UserID)

	groupID := harness.CreateGroup(t, owner, "边界工作组")
	projectID := harness.CreateProject(t, owner, groupID, "边界项目")
	taskID := harness.CreateTask(t, owner, projectID, "边界任务", "2026-10-01", "2026-10-05")

	// 1) 归档项目对管理员同样只读（这是状态规则，不是权限）。
	version := Str(owner.Client.Get(URL("/projects/"+projectID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	admin.Client.Post(URL("/projects/"+projectID+"/archive"), map[string]any{
		"expected_updated_at": version,
	}).RequireStatus(t, 200)

	taskVersion := Str(owner.Client.Get(URL("/tasks/"+taskID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	admin.Client.Patch(URL("/tasks/"+taskID), map[string]any{
		"name":                "管理员也改不了归档项目的任务",
		"expected_updated_at": taskVersion,
	}).RequireStatus(t, 409)

	// 2) 唯一持有者不可降级/移除，对管理员同样生效。
	//
	// 用**未归档**的项目：归档项目会更早一步以 409 拒绝，
	// 那样测到的是归档规则而不是持有者不变式。
	liveProject := harness.CreateProject(t, owner, groupID, "边界项目（未归档）")
	admin.Client.Patch(URL("/projects/"+liveProject+"/members/"+owner.UserID),
		map[string]any{"role": "member"}).RequireStatus(t, 403)
	admin.Client.Delete(URL("/projects/"+liveProject+"/members/"+owner.UserID), nil).
		RequireStatus(t, 403)

	// 3) 任务成员必须是项目成员：管理员也不能把陌生人直接塞进任务。
	//    同样用未归档项目里的任务——归档项目会先以 403 拦住写入。
	liveTask := harness.CreateTask(t, owner, liveProject, "边界任务（未归档）", "2026-10-01", "2026-10-05")
	outsider := harness.RegisterAndLogin(t, "bound_outsider")
	admin.Client.Post(URL("/tasks/"+liveTask+"/members"),
		map[string]any{"user_id": outsider.UserID}).
		RequireStatus(t, 422).RequireField(t, "user_id")

	// 4) 工作组所有者不可被降级或移除（groups.owner_id 的不变式）。
	admin.Client.Patch(URL("/groups/"+groupID+"/members/"+owner.UserID), map[string]any{
		"role": "member",
	}).RequireStatus(t, 403)
	admin.Client.Delete(URL("/groups/"+groupID+"/members/"+owner.UserID), nil).
		RequireStatus(t, 403)
}

// TestAdminCanTransferOwnership 覆盖"管理员是所有权转移的兜底"。
//
// 这是本轮修复的直接目标：持有者离职或被停用时，只有管理员能接管。
func TestAdminCanTransferOwnership(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "tr_owner")
	successor := harness.RegisterAndLogin(t, "tr_successor")
	admin := harness.RegisterAndLogin(t, "tr_admin")
	promoteToAdmin(t, harness, admin.UserID)

	groupID := harness.CreateGroup(t, owner, "兜底转移工作组")
	projectID := harness.CreateProject(t, owner, groupID, "兜底转移项目")
	taskID := harness.CreateTask(t, owner, projectID, "兜底转移任务", "2026-10-01", "2026-10-05")

	// 目标必须是成员：转移持有者不会顺带把人加进来。
	owner.Client.Post(URL("/projects/"+projectID+"/members"),
		map[string]any{"user_id": successor.UserID}).RequireStatus(t, 201)
	owner.Client.Post(URL("/tasks/"+taskID+"/members"),
		map[string]any{"user_id": successor.UserID}).RequireStatus(t, 201)

	// 项目：管理员（非持有者、非成员）可以转移。
	admin.Client.Patch(URL("/projects/"+projectID+"/members/"+successor.UserID),
		map[string]any{"role": "owner"}).RequireStatus(t, 200)
	if got := countOwners(t, harness, "project_members", "project_id", projectID); got != 1 {
		t.Fatalf("项目持有者数量 = %d，期望 1", got)
	}

	// 任务：管理员同样可以转移。
	admin.Client.Patch(URL("/tasks/"+taskID+"/members/"+successor.UserID),
		map[string]any{"role": "owner"}).RequireStatus(t, 200)
	if got := countOwners(t, harness, "task_members", "task_id", taskID); got != 1 {
		t.Fatalf("任务持有者数量 = %d，期望 1", got)
	}

	// 工作组：走 PATCH /groups/{id} 的 owner_id。目标必须是**该组的**活跃成员——
	// 项目成员身份不构成组成员身份。
	owner.Client.Post(URL("/groups/"+groupID+"/members"),
		map[string]any{"user_id": successor.UserID}).RequireStatus(t, 201)
	groupVersion := Str(admin.Client.Get(URL("/groups/"+groupID)).
		RequireStatus(t, 200).Data(t), "updated_at")
	transferred := admin.Client.Patch(URL("/groups/"+groupID), map[string]any{
		"owner_id":            successor.UserID,
		"expected_updated_at": groupVersion,
	})
	transferred.RequireStatus(t, 200)
	if got := Str(transferred.Data(t), "owner_id"); got != successor.UserID {
		t.Fatalf("工作组 owner_id = %q，期望 %q", got, successor.UserID)
	}
}

// TestGroupOwnershipTransfer 覆盖工作组所有权转移的语义与拒绝路径。
func TestGroupOwnershipTransfer(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "gt_owner")
	manager := harness.RegisterAndLogin(t, "gt_manager")
	member := harness.RegisterAndLogin(t, "gt_member")
	stranger := harness.RegisterAndLogin(t, "gt_stranger")
	disabled := harness.RegisterAndLogin(t, "gt_disabled")

	groupID := harness.CreateGroup(t, owner, "所有权转移工作组")
	harness.AddGroupMember(t, owner, groupID, manager.UserID)
	harness.AddGroupMember(t, owner, groupID, member.UserID)
	harness.AddGroupMember(t, owner, groupID, disabled.UserID)

	// manager 提升为组管理员（他仍**不能**转移所有权）。
	owner.Client.Patch(URL("/groups/"+groupID+"/members/"+manager.UserID),
		map[string]any{"role": "manager"}).RequireStatus(t, 200)

	versionOf := func() string {
		return Str(owner.Client.Get(URL("/groups/"+groupID)).
			RequireStatus(t, 200).Data(t), "updated_at")
	}
	transfer := func(client *Client, target string, version string) Response {
		return client.Patch(URL("/groups/"+groupID), map[string]any{
			"owner_id":            target,
			"expected_updated_at": version,
		})
	}

	// 组管理员不能转移所有权（这是治理决定，不是日常成员管理）。
	transfer(manager.Client, member.UserID, versionOf()).RequireStatus(t, 403)

	// 普通成员与非成员同样不能。
	transfer(member.Client, member.UserID, versionOf()).RequireStatus(t, 403)
	transfer(stranger.Client, member.UserID, versionOf()).RequireStatus(t, 403)

	// 目标必须是活跃成员。
	transfer(owner.Client, stranger.UserID, versionOf()).
		RequireStatus(t, 422).RequireField(t, "owner_id")

	// 目标不能是已停用用户：否则组会立刻进入"谁都改不了"的状态。
	if _, err := harness.Pool.Exec(context.Background(),
		`UPDATE users SET status = 'disabled', updated_at = now() WHERE id = $1`,
		disabled.UserID); err != nil {
		t.Fatalf("停用用户失败: %v", err)
	}
	transfer(owner.Client, disabled.UserID, versionOf()).
		RequireStatus(t, 422).RequireField(t, "owner_id")

	// 目标已经是所有者：409，而不是"成功但没有变化"。
	transfer(owner.Client, owner.UserID, versionOf()).RequireStatus(t, 409)

	// 正常转移。
	transferred := transfer(owner.Client, member.UserID, versionOf()).RequireStatus(t, 200)
	if got := Str(transferred.Data(t), "owner_id"); got != member.UserID {
		t.Fatalf("owner_id = %q，期望 %q", got, member.UserID)
	}

	// 投影一致：新所有者必须是该组的活跃组管理员（docs/02 §3.2.1）。
	var role, status string
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT role, status FROM group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, member.UserID).Scan(&role, &status); err != nil {
		t.Fatalf("查询新所有者成员关系失败: %v", err)
	}
	if role != "manager" || status != "active" {
		t.Fatalf("新所有者的成员关系 = (%s, %s)，期望 (manager, active)", role, status)
	}

	// 原所有者降为普通成员：所有权转移就是交出管理权。
	if err := harness.Pool.QueryRow(context.Background(),
		`SELECT role FROM group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, owner.UserID).Scan(&role); err != nil {
		t.Fatalf("查询原所有者成员关系失败: %v", err)
	}
	if role != "member" {
		t.Fatalf("原所有者角色 = %q，期望 member", role)
	}

	// 原所有者失去管理权，新所有者获得管理权。
	owner.Client.Post(URL("/groups/"+groupID+"/members"),
		map[string]any{"user_id": stranger.UserID}).RequireStatus(t, 404)
	member.Client.Post(URL("/groups/"+groupID+"/members"),
		map[string]any{"user_id": stranger.UserID}).RequireStatus(t, 201)

	// 新所有者不能被降级或移除（不变式随所有权一起转移）。
	member.Client.Delete(URL("/groups/"+groupID+"/members/"+member.UserID), nil).
		RequireStatus(t, 403)

	// 版本冲突仍然生效。
	member.Client.Patch(URL("/groups/"+groupID), map[string]any{
		"name":                "用过期版本改名",
		"expected_updated_at": "2020-01-01T00:00:00Z",
	}).RequireStatus(t, 409)
}

// countOwners 统计某张成员表里 role='owner' 的行数。
func countOwners(t *testing.T, harness *Harness, table, column, id string) int {
	t.Helper()

	// table/column 只来自本文件的字面量，不涉及用户输入。
	var count int
	query := `SELECT count(*) FROM ` + table + ` WHERE ` + column + ` = $1 AND role = 'owner'`
	if err := harness.Pool.QueryRow(context.Background(), query, id).Scan(&count); err != nil {
		t.Fatalf("统计持有者失败: %v", err)
	}
	return count
}

// containsRecord 报告列表中是否含指定 id。
func containsRecord(items []map[string]any, id string) bool {
	for _, item := range items {
		if Str(item, "id") == id {
			return true
		}
	}
	return false
}
