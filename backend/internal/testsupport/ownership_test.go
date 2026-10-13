package testsupport

import (
	"context"
	"testing"
	"time"
)

// 持有者转移的并发安全：无论两个转移请求如何交错，结束后必须**恰好一个**持有者。
//
// 为什么单独写一条：转移由两条语句组成（先降现任、再升目标），
// "恰好一个"这个不变式既可能被"降了但没升"破坏（0 个），也可能被
// "升了但没降干净"破坏（2 个）。后一种情况数据库的部分唯一索引会拦住，
// 前一种只能靠事务保证——而它恰恰是最危险的：0 个持有者意味着项目永远无人可管理。
//
// 用外部会话先占住现任持有者的行锁，强制两个请求在**写入前**排队，
// 否则它们很可能被调度成一先一后，测不到真正的交错（这个教训在第三阶段反复出现过）。
func TestOwnershipTransferConcurrencyKeepsExactlyOneOwner(t *testing.T) {
	harness := NewHarness(t)

	owner := harness.RegisterAndLogin(t, "transfer_race_owner")
	first := harness.RegisterAndLogin(t, "transfer_race_first")
	second := harness.RegisterAndLogin(t, "transfer_race_second")

	groupID := harness.CreateGroup(t, owner, "转移并发工作组")
	projectID := harness.CreateProject(t, owner, groupID, "转移并发项目")

	for _, member := range []Authenticated{first, second} {
		owner.Client.Post(URL("/projects/"+projectID+"/members"),
			map[string]any{"user_id": member.UserID}).RequireStatus(t, 201)
	}

	ctx := context.Background()
	conn, err := harness.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取数据库连接失败: %v", err)
	}
	defer conn.Release()

	blocker, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("开启占锁事务失败: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()

	// 占住现任持有者的成员行：两个转移请求的第一个写语句都要动这一行。
	if _, err := blocker.Exec(ctx,
		`SELECT id FROM project_members WHERE project_id = $1 AND role = 'owner' FOR UPDATE`,
		projectID); err != nil {
		t.Fatalf("占用持有者行锁失败: %v", err)
	}

	type outcome struct {
		status int
		body   []byte
	}
	results := make(chan outcome, 2)
	for _, member := range []Authenticated{first, second} {
		go func(member Authenticated) {
			response := owner.Client.Patch(URL("/projects/"+projectID+"/members/"+member.UserID),
				map[string]any{"role": "owner"})
			results <- outcome{status: response.Status, body: response.Body}
		}(member)
	}

	// 锁被占用期间不应有请求完成。
	select {
	case result := <-results:
		t.Fatalf("行锁被占用期间转移请求已返回（状态码 %d，响应体 %s）",
			result.status, string(result.body))
	case <-time.After(500 * time.Millisecond):
	}

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("释放行锁失败: %v", err)
	}

	// 两个请求都可能成功（"最后写入者接管"），也可能一个冲突；两者都可接受。
	// 不可接受的是持有者数量不为 1。
	for range 2 {
		select {
		case result := <-results:
			if result.status != 200 && result.status != 409 {
				t.Fatalf("并发转移返回了非预期状态码 %d：%s", result.status, string(result.body))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("并发转移请求未在超时内完成")
		}
	}

	var owners int
	if err := harness.Pool.QueryRow(ctx,
		`SELECT count(*) FROM project_members WHERE project_id = $1 AND role = 'owner'`,
		projectID).Scan(&owners); err != nil {
		t.Fatalf("统计持有者失败: %v", err)
	}
	if owners != 1 {
		t.Fatalf("并发转移后持有者数量 = %d，期望恰好 1（0 个则项目永久无人可管理）", owners)
	}
}
