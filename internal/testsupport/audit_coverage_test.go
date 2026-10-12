package testsupport

import (
	"context"
	"strings"
	"testing"
)

// 本文件是**写路径覆盖**测试：证明各业务域的关键写操作确实在自己的事务里
// 写下了操作记录（docs/06 §5.17 的覆盖矩阵）。
//
// 为什么必须逐域写：审计的接入点分散在 30 余处写路径上，漏掉一处不会报错，
// 只会让某个操作没有历史。因此每个域都要有一条"做了什么 ⇒ 记录里有什么"的用例，
// 而不是只测审计模块自身。

// auditRow 是一条操作记录的测试投影。
type auditRow struct {
	Action    string
	ActorID   string
	Changes   string
	RequestID string
}

// auditRowsFor 读取某个对象的全部操作记录。
//
// 刻意不按 created_at 排序：同一测试内的记录时间由数据库 now() 生成，
// 可能落在同一微秒；UUIDv7 在固定时钟下也只有随机位不同，因此任何"按时间排序后取第 N 条"
// 的断言都不稳。这里按集合断言（"存在这样一条"），把顺序问题彻底去掉。
func auditRowsFor(t *testing.T, harness *Harness, objectID string) []auditRow {
	t.Helper()

	rows, err := harness.Pool.Query(context.Background(),
		`SELECT action, actor_id, changes::text, request_id
		   FROM activity_logs
		  WHERE object_id = $1`, objectID)
	if err != nil {
		t.Fatalf("查询操作记录失败: %v", err)
	}
	defer rows.Close()

	var result []auditRow
	for rows.Next() {
		var row auditRow
		if err := rows.Scan(&row.Action, &row.ActorID, &row.Changes, &row.RequestID); err != nil {
			t.Fatalf("读取操作记录失败: %v", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历操作记录失败: %v", err)
	}
	return result
}

// requireAuditRow 断言"存在一条指定动作的记录，并且它满足全部附加条件"。
func requireAuditRow(t *testing.T, rows []auditRow, action string, contains ...string) auditRow {
	t.Helper()

	for _, row := range rows {
		if row.Action != action {
			continue
		}
		matched := true
		for _, fragment := range contains {
			if !strings.Contains(row.Changes, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return row
		}
	}
	t.Fatalf("没有找到动作 %q 且包含 %v 的记录；实际记录：%+v", action, contains, rows)
	return auditRow{}
}

// countAuditRowsFor 统计某个对象的记录条数。
func countAuditRowsFor(t *testing.T, harness *Harness, objectID string) int {
	t.Helper()
	return len(auditRowsFor(t, harness, objectID))
}

// clearAuditRows 清空操作记录表。
//
// 为什么需要它：注册、创建工作组等**前置动作本身也会写审计**（这是本次实现的要求）。
// 只关心查询层语义的用例需要一个可控的起点，因此在造数据之前先清空，
// 否则断言里会混进"注册"这类与用例目标无关的记录。
func clearAuditRows(t *testing.T, harness *Harness) {
	t.Helper()

	if _, err := harness.Pool.Exec(context.Background(), `DELETE FROM activity_logs`); err != nil {
		t.Fatalf("清空操作记录失败: %v", err)
	}
}

// TestAuditCoversProjectWrites 覆盖项目域的六类关键写操作。
func TestAuditCoversProjectWrites(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "audit_proj_owner")
	first := harness.RegisterAndLogin(t, "audit_proj_first")
	second := harness.RegisterAndLogin(t, "audit_proj_second")
	groupID := harness.CreateGroup(t, owner, "审计覆盖组")
	projectID := harness.CreateProject(t, owner, groupID, "审计覆盖项目")

	rows := auditRowsFor(t, harness, projectID)
	created := requireAuditRow(t, rows, "created", "审计覆盖项目")

	if created.ActorID != owner.UserID {
		t.Fatalf("created 记录的 actor_id = %s，期望 %s", created.ActorID, owner.UserID)
	}
	if created.RequestID == "" {
		t.Fatal("created 记录缺少 request_id（说明请求链路没有把 request id 传到用例层）")
	}

	// PATCH：改名 + 共享级别（一次请求产生 updated 与 shared 两条记录）。
	current := owner.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200)
	updatedAt := Str(current.Data(t), "updated_at")

	owner.Client.Patch(URL("/projects/"+projectID), map[string]any{
		"name":                "审计覆盖项目（改名）",
		"visibility":          "group_readonly",
		"expected_updated_at": updatedAt,
	}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, projectID)
	requireAuditRow(t, rows, "updated", "审计覆盖项目（改名）")
	requireAuditRow(t, rows, "shared", "group_readonly")

	// 成员增删与持有者转移。
	harness.AddProjectMember(t, owner, projectID, first.UserID)
	harness.AddProjectMember(t, owner, projectID, second.UserID)
	rows = auditRowsFor(t, harness, projectID)
	requireAuditRow(t, rows, "member_added", first.UserID)
	requireAuditRow(t, rows, "member_added", second.UserID)

	owner.Client.Patch(URL("/projects/"+projectID+"/members/"+first.UserID),
		map[string]any{"role": "owner"}).RequireStatus(t, 200)
	rows = auditRowsFor(t, harness, projectID)
	requireAuditRow(t, rows, "owner_transferred", first.UserID)

	transferred := first.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200)
	if Str(transferred.Data(t), "membership_role") != "owner" {
		t.Fatalf("转移后 first 的角色 = %s，期望 owner", Str(transferred.Data(t), "membership_role"))
	}

	// 转移后原持有者已降为 member，因此移除成员要由新持有者发起。
	first.Client.Delete(URL("/projects/"+projectID+"/members/"+second.UserID), nil).
		RequireStatus(t, 204)
	rows = auditRowsFor(t, harness, projectID)
	requireAuditRow(t, rows, "member_removed", second.UserID)

	// 归档：用当前版本。
	current = first.Client.Get(URL("/projects/"+projectID)).RequireStatus(t, 200)
	updatedAt = Str(current.Data(t), "updated_at")
	first.Client.Post(URL("/projects/"+projectID+"/archive"),
		map[string]any{"expected_updated_at": updatedAt}).RequireStatus(t, 200)

	rows = auditRowsFor(t, harness, projectID)
	requireAuditRow(t, rows, "archived")

	// 失败的写入不得留下记录：版本冲突。
	before := countAuditRowsFor(t, harness, projectID)
	first.Client.Patch(URL("/projects/"+projectID), map[string]any{
		"name":                "不应生效",
		"expected_updated_at": updatedAt,
	}).RequireStatus(t, 409)
	if after := countAuditRowsFor(t, harness, projectID); after != before {
		t.Fatalf("版本冲突的写入留下了 %d 条新记录（期望 0）", after-before)
	}

	// 归档项目的写入被拒时同样不留记录。
	archiveBefore := countAuditRowsFor(t, harness, projectID)
	first.Client.Delete(URL("/projects/"+projectID+"/members/"+first.UserID), nil).
		RequireStatus(t, 409)
	if after := countAuditRowsFor(t, harness, projectID); after != archiveBefore {
		t.Fatalf("被拒的归档项目写入留下了 %d 条新记录（期望 0）", after-archiveBefore)
	}
}
