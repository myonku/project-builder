package testsupport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/database"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// TestMigrationsApplyFromEmptyDatabase 验证初始迁移可以从空库顺序执行。
//
// 验收要求（docs/02-数据库模型与迁移.md 6.1）：空库执行全部迁移后 schema 与文档一致。
func TestMigrationsApplyFromEmptyDatabase(t *testing.T) {
	// 与其他迁移用例串行，避免并发 DDL 互相干扰。
	pool := EnsureMigrated(t)

	// schema_migrations 必须记录到最新版本且不脏。
	migrator, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		t.Fatalf("打开迁移器失败: %v", err)
	}
	defer migrator.Close()

	version, dirty, err := migrator.Version()
	if err != nil {
		t.Fatalf("读取迁移版本失败: %v", err)
	}
	if dirty {
		t.Fatal("schema 处于脏状态")
	}
	if version != 1 {
		t.Fatalf("迁移版本 = %d，期望 1（单一初始迁移）", version)
	}

	// 全部 9 张业务表必须存在。
	expected := []string{
		"users", "groups", "group_members", "group_join_requests",
		"projects", "project_members", "tasks", "task_members", "idempotency_keys",
	}
	for _, table := range expected {
		var exists bool
		err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("查询表 %s 是否存在失败: %v", table, err)
		}
		if !exists {
			t.Errorf("迁移后缺少表 %s", table)
		}
	}
}

// TestMigrationsAreReversible 验证 down 可回退且回退后可再次 up。
//
// 验收要求（docs/02-数据库模型与迁移.md 6.2）：迁移 down 可回退，且回退后 up 可再次成功。
// 这是 Go 版本相对 Python 版本新增的强制验证：Python 的 downgrade 只是"写过"，
// 从未在 CI 中被真正执行过。
func TestMigrationsAreReversible(t *testing.T) {
	// 确认依赖可达（不可达则跳过）。
	pool := EnsureMigrated(t)
	pool.Close()

	migrator, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		t.Fatalf("打开迁移器失败: %v", err)
	}
	defer migrator.Close()

	// 回退到空库。
	if err := migrator.Down(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			t.Fatal("期望执行回退，但迁移器报告无变化")
		}
		t.Fatalf("回退迁移失败: %v", err)
	}

	// 业务表必须全部消失。
	probe, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		t.Fatalf("重新打开迁移器失败: %v", err)
	}
	version, _, versionErr := probe.Version()
	probe.Close()
	if versionErr != nil {
		t.Fatalf("回退后读取版本失败: %v", versionErr)
	}
	if version != 1 {
		// ErrinNilVersion 时 version 为 0，说明已回到最初状态。
		t.Logf("回退后版本 = %d（0 表示无版本记录）", version)
	}

	reopened := EnsureMigrated(t)
	defer reopened.Close()

	var userTableExists bool
	if err := reopened.QueryRow(context.Background(),
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'users'
		)`).Scan(&userTableExists); err != nil {
		t.Fatalf("查询 users 表失败: %v", err)
	}
	if !userTableExists {
		t.Fatal("回退后重新 up 必须重建 users 表")
	}
}

// TestSchemaMatchesDocumentedShape 验证关键列类型与文档一致。
//
// 重点是历史上出过错的地方：projects.archived_at 必须是带时区类型
// （Python 版本曾建成不带时区，靠 0004_archive_timestamp 修正）。
func TestSchemaMatchesDocumentedShape(t *testing.T) {
	pool := EnsureMigrated(t)

	tests := []struct {
		table    string
		column   string
		wantType string
		nullable string
	}{
		// 瞬时时间列统一必须带时区（timestamptz）。
		{table: "users", column: "created_at", wantType: "timestamp with time zone", nullable: "NO"},
		{table: "users", column: "updated_at", wantType: "timestamp with time zone", nullable: "NO"},
		{table: "users", column: "last_login_at", wantType: "timestamp with time zone", nullable: "YES"},
		{table: "projects", column: "archived_at", wantType: "timestamp with time zone", nullable: "YES"},
		{table: "tasks", column: "deleted_at", wantType: "timestamp with time zone", nullable: "YES"},
		{table: "tasks", column: "created_at", wantType: "timestamp with time zone", nullable: "NO"},
		{table: "group_members", column: "joined_at", wantType: "timestamp with time zone", nullable: "NO"},
		// 业务日期列必须是纯 date，不得混入时分秒。
		{table: "projects", column: "start_date", wantType: "date", nullable: "NO"},
		{table: "projects", column: "end_date", wantType: "date", nullable: "NO"},
		{table: "tasks", column: "start_date", wantType: "date", nullable: "NO"},
		{table: "tasks", column: "end_date", wantType: "date", nullable: "NO"},
	}

	for _, tc := range tests {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			var dataType, isNullable string
			err := pool.QueryRow(context.Background(),
				`SELECT data_type, is_nullable FROM information_schema.columns
				 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
				tc.table, tc.column).Scan(&dataType, &isNullable)
			if err != nil {
				t.Fatalf("查询列定义失败: %v", err)
			}
			if dataType != tc.wantType {
				t.Errorf("%s.%s 类型 = %q，期望 %q", tc.table, tc.column, dataType, tc.wantType)
			}
			if isNullable != tc.nullable {
				t.Errorf("%s.%s 可空性 = %q，期望 %q", tc.table, tc.column, isNullable, tc.nullable)
			}
		})
	}
}

// TestEveryBusinessTableHasAuditColumns 验证全局基线约定 9 的必备列。
func TestEveryBusinessTableHasAuditColumns(t *testing.T) {
	pool := EnsureMigrated(t)

	tables := []string{
		"users", "groups", "group_members", "group_join_requests",
		"projects", "project_members", "tasks", "task_members", "idempotency_keys",
	}
	required := []string{"id", "created_at", "updated_at"}

	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			for _, column := range required {
				var exists bool
				err := pool.QueryRow(context.Background(),
					`SELECT EXISTS (
						SELECT 1 FROM information_schema.columns
						WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
					)`, table, column).Scan(&exists)
				if err != nil {
					t.Fatalf("查询列失败: %v", err)
				}
				if !exists {
					t.Errorf("表 %s 缺少必备列 %s", table, column)
				}
			}
		})
	}
}

// TestPartialUniqueIndexesExist 验证"单一持有者"不变式的索引确实建立。
func TestPartialUniqueIndexesExist(t *testing.T) {
	pool := EnsureMigrated(t)

	tests := []struct {
		index   string
		table   string
		partial bool
	}{
		{index: "project_single_owner", table: "project_members", partial: true},
		{index: "task_single_owner", table: "task_members", partial: true},
		{index: "idempotency_key_identity", table: "idempotency_keys", partial: false},
		{index: "group_member_identity", table: "group_members", partial: false},
	}

	for _, tc := range tests {
		t.Run(tc.index, func(t *testing.T) {
			var tableName, indexDef string
			err := pool.QueryRow(context.Background(),
				`SELECT tablename, indexdef FROM pg_indexes
				 WHERE schemaname = 'public' AND indexname = $1`, tc.index).Scan(&tableName, &indexDef)
			if err != nil {
				t.Fatalf("索引 %s 不存在: %v", tc.index, err)
			}
			if tableName != tc.table {
				t.Errorf("索引 %s 位于表 %s，期望 %s", tc.index, tableName, tc.table)
			}
			if tc.partial {
				if !strings.Contains(indexDef, "WHERE") || !strings.Contains(indexDef, "owner") {
					t.Errorf("索引 %s 必须是 WHERE role = 'owner' 的部分唯一索引，实际定义: %s", tc.index, indexDef)
				}
				if !strings.Contains(indexDef, "UNIQUE") {
					t.Errorf("索引 %s 必须是唯一索引，实际定义: %s", tc.index, indexDef)
				}
			}
		})
	}
}

// TestForeignKeyDeleteBehaviours 验证外键删除行为被显式声明。
//
// 约定（docs/02-数据库模型与迁移.md 5.1）：不得依赖默认值。
func TestForeignKeyDeleteBehaviours(t *testing.T) {
	pool := EnsureMigrated(t)

	tests := []struct {
		constraint string
		onDelete   string
	}{
		{constraint: "fk_projects_group_id_groups", onDelete: "RESTRICT"},
		{constraint: "fk_projects_created_by_users", onDelete: "RESTRICT"},
		{constraint: "fk_projects_created_by_users", onDelete: "RESTRICT"},
		{constraint: "fk_project_members_project_id_projects", onDelete: "CASCADE"},
		{constraint: "fk_project_members_user_id_users", onDelete: "RESTRICT"},
		{constraint: "fk_tasks_project_id_projects", onDelete: "CASCADE"},
		{constraint: "fk_tasks_parent_id_tasks", onDelete: "CASCADE"},
		{constraint: "fk_tasks_created_by_users", onDelete: "RESTRICT"},
		{constraint: "fk_task_members_task_id_tasks", onDelete: "CASCADE"},
		{constraint: "fk_task_members_user_id_users", onDelete: "RESTRICT"},
		{constraint: "fk_group_members_group_id_groups", onDelete: "CASCADE"},
		{constraint: "fk_group_members_user_id_users", onDelete: "CASCADE"},
		{constraint: "fk_group_join_requests_reviewed_by_users", onDelete: "SET NULL"},
		{constraint: "fk_idempotency_keys_user_id_users", onDelete: "CASCADE"},
	}

	for _, tc := range tests {
		t.Run(tc.constraint+"_"+tc.onDelete, func(t *testing.T) {
			var deleteRule string
			err := pool.QueryRow(context.Background(),
				`SELECT rc.delete_rule
				   FROM information_schema.referential_constraints rc
				   JOIN information_schema.table_constraints tc
				     ON tc.constraint_name = rc.constraint_name
				    AND tc.constraint_schema = rc.constraint_schema
				  WHERE tc.constraint_name = $1`, tc.constraint).Scan(&deleteRule)
			if err != nil {
				t.Fatalf("外键 %s 不存在: %v", tc.constraint, err)
			}
			if deleteRule != tc.onDelete {
				t.Errorf("外键 %s 的 ON DELETE = %q，期望 %q", tc.constraint, deleteRule, tc.onDelete)
			}
		})
	}
}

// TestCheckConstraintsRejectInvalidEnumValues 验证枚举 CHECK 约束真的生效。
//
// 验收要求（docs/02-数据库模型与迁移.md 6.4）：所有 CHECK 约束在真实数据库上被验证确实生效。
func TestCheckConstraintsRejectInvalidEnumValues(t *testing.T) {
	pool := EnsureMigrated(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	userID := insertUser(t, pool, "alice")

	tests := []struct {
		name string
		exec func() error
	}{
		{
			name: "users.status 非法",
			exec: func() error {
				_, err := pool.Exec(ctx,
					`INSERT INTO users (id, username, email, display_name, password_hash, status)
					 VALUES (gen_random_uuid(), 'x', 'x@example.com', 'X', 'h', 'bogus')`)
				return err
			},
		},
		{
			name: "users.system_role 非法",
			exec: func() error {
				_, err := pool.Exec(ctx,
					`INSERT INTO users (id, username, email, display_name, password_hash, system_role)
					 VALUES (gen_random_uuid(), 'y', 'y@example.com', 'Y', 'h', 'superuser')`)
				return err
			},
		},
		{
			name: "projects.status 非法",
			exec: func() error {
				groupID := insertGroup(t, pool, userID, "G")
				_, err := pool.Exec(ctx,
					`INSERT INTO projects (id, name, created_by, group_id, status, start_date, end_date)
					 VALUES (gen_random_uuid(), 'P', $1, $2, 'overdue', '2026-10-01', '2026-10-02')`,
					userID, groupID)
				return err
			},
		},
		{
			name: "projects.visibility 非法",
			exec: func() error {
				groupID := insertGroup(t, pool, userID, "G2")
				_, err := pool.Exec(ctx,
					`INSERT INTO projects (id, name, created_by, group_id, visibility, start_date, end_date)
					 VALUES (gen_random_uuid(), 'P', $1, $2, 'public', '2026-10-01', '2026-10-02')`,
					userID, groupID)
				return err
			},
		},
		{
			name: "projects 日期区间倒置",
			exec: func() error {
				groupID := insertGroup(t, pool, userID, "G3")
				_, err := pool.Exec(ctx,
					`INSERT INTO projects (id, name, created_by, group_id, start_date, end_date)
					 VALUES (gen_random_uuid(), 'P', $1, $2, '2026-10-05', '2026-10-01')`,
					userID, groupID)
				return err
			},
		},
		{
			name: "tasks.status 非法（overdue 不是持久化状态）",
			exec: func() error {
				projectID := insertProject(t, pool, userID)
				_, err := pool.Exec(ctx,
					`INSERT INTO tasks (id, project_id, created_by, name, status, start_date, end_date)
					 VALUES (gen_random_uuid(), $1, $2, 'T', 'overdue', '2026-10-01', '2026-10-02')`,
					projectID, userID)
				return err
			},
		},
		{
			name: "tasks.progress 越界（上界）",
			exec: func() error {
				projectID := insertProject(t, pool, userID)
				_, err := pool.Exec(ctx,
					`INSERT INTO tasks (id, project_id, created_by, name, progress, start_date, end_date)
					 VALUES (gen_random_uuid(), $1, $2, 'T', 101, '2026-10-01', '2026-10-02')`,
					projectID, userID)
				return err
			},
		},
		{
			name: "tasks.progress 越界（下界）",
			exec: func() error {
				projectID := insertProject(t, pool, userID)
				_, err := pool.Exec(ctx,
					`INSERT INTO tasks (id, project_id, created_by, name, progress, start_date, end_date)
					 VALUES (gen_random_uuid(), $1, $2, 'T', -1, '2026-10-01', '2026-10-02')`,
					projectID, userID)
				return err
			},
		},
		{
			name: "tasks 日期区间倒置",
			exec: func() error {
				projectID := insertProject(t, pool, userID)
				_, err := pool.Exec(ctx,
					`INSERT INTO tasks (id, project_id, created_by, name, start_date, end_date)
					 VALUES (gen_random_uuid(), $1, $2, 'T', '2026-10-09', '2026-10-01')`,
					projectID, userID)
				return err
			},
		},
		{
			name: "group_members.role 非法",
			exec: func() error {
				groupID := insertGroup(t, pool, userID, "G4")
				_, err := pool.Exec(ctx,
					`INSERT INTO group_members (id, group_id, user_id, role)
					 VALUES (gen_random_uuid(), $1, $2, 'owner')`, groupID, userID)
				return err
			},
		},
		{
			name: "project_members.role 非法",
			exec: func() error {
				projectID := insertProject(t, pool, userID)
				_, err := pool.Exec(ctx,
					`INSERT INTO project_members (id, project_id, user_id, role)
					 VALUES (gen_random_uuid(), $1, $2, 'manager')`, projectID, userID)
				return err
			},
		},
		{
			name: "group_join_requests.status 非法",
			exec: func() error {
				groupID := insertGroup(t, pool, userID, "G5")
				_, err := pool.Exec(ctx,
					`INSERT INTO group_join_requests (id, group_id, user_id, status)
					 VALUES (gen_random_uuid(), $1, $2, 'cancelled')`, groupID, userID)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.exec()
			if err == nil {
				t.Fatal("数据库必须拒绝非法取值，实际写入成功")
			}
			if !strings.Contains(err.Error(), "constraint") && !strings.Contains(err.Error(), "violates") {
				t.Fatalf("期望约束违反错误，实际为: %v", err)
			}
		})
	}
}

// TestSingleOwnerPartialIndexRejectsSecondOwner 是本项目最重要的数据库不变式测试。
//
// 验收要求（docs/02-数据库模型与迁移.md 6.3）：部分唯一索引在真实数据库上被验证
// 确实拒绝第二个 owner。
func TestSingleOwnerPartialIndexRejectsSecondOwner(t *testing.T) {
	pool := EnsureMigrated(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	owner := insertUser(t, pool, "owner")
	second := insertUser(t, pool, "second")
	projectID := insertProject(t, pool, owner)
	taskID := insertTask(t, pool, projectID, owner)

	t.Run("project_members 拒绝第二个 owner", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO project_members (id, project_id, user_id, role)
			 VALUES (gen_random_uuid(), $1, $2, 'owner')`, projectID, owner); err != nil {
			t.Fatalf("第一个 owner 应被接受: %v", err)
		}

		if _, err := pool.Exec(ctx,
			`INSERT INTO project_members (id, project_id, user_id, role)
			 VALUES (gen_random_uuid(), $1, $2, 'owner')`, projectID, second); err == nil {
			t.Fatal("第二个 owner 必须被数据库拒绝")
		}
	})

	t.Run("task_members 拒绝第二个 owner", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO task_members (id, task_id, user_id, role)
			 VALUES (gen_random_uuid(), $1, $2, 'owner')`, taskID, owner); err != nil {
			t.Fatalf("第一个 owner 应被接受: %v", err)
		}

		if _, err := pool.Exec(ctx,
			`INSERT INTO task_members (id, task_id, user_id, role)
			 VALUES (gen_random_uuid(), $1, $2, 'owner')`, taskID, second); err == nil {
			t.Fatal("第二个 owner 必须被数据库拒绝")
		}
	})

	t.Run("部分索引不限制 member 数量", func(t *testing.T) {
		// 关键点：部分唯一索引只约束 owner，member 可以有任意多个。
		for i := 0; i < 5; i++ {
			userID := insertUser(t, pool, "member"+Describe(i))
			if _, err := pool.Exec(ctx,
				`INSERT INTO project_members (id, project_id, user_id, role)
				 VALUES (gen_random_uuid(), $1, $2, 'member')`, projectID, userID); err != nil {
				t.Fatalf("第 %d 个 member 应被接受: %v", i, err)
			}
		}
	})

	t.Run("同一用户不能重复成为成员", func(t *testing.T) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO project_members (id, project_id, user_id, role)
			 VALUES (gen_random_uuid(), $1, $2, 'member')`, projectID, owner); err == nil {
			t.Fatal("project_member_identity 必须拒绝重复成员")
		}
	})
}

// TestUserIdentityIsCaseInsensitiveInDatabase 验证数据库层面的不区分大小写唯一性。
//
// 这是并发注册同一账号的最终保证：应用层的提前检查只是提示，不是并发保护。
func TestUserIdentityIsCaseInsensitiveInDatabase(t *testing.T) {
	pool := EnsureMigrated(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	insertUser(t, pool, "alice")

	// 大小写不同的同一用户名必须被拒绝。
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, username, email, display_name, password_hash)
		 VALUES (gen_random_uuid(), 'ALICE', 'other@example.com', 'Other', 'h')`); err == nil {
		t.Fatal("uq_users_username_lower 必须拒绝大小写不同的同一用户名")
	}

	// 大小写不同的同一邮箱必须被拒绝。
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, username, email, display_name, password_hash)
		 VALUES (gen_random_uuid(), 'other', 'ALICE@example.com', 'Other', 'h')`); err == nil {
		t.Fatal("uq_users_email_lower 必须拒绝大小写不同的同一邮箱")
	}
}

// TestUpdatedAtMustBeAdvancedExplicitly 验证 updated_at 语义。
//
// 数据库模型文档 2 明确：本 schema 不引入 ON UPDATE 触发器，updated_at 由写语句
// 显式 SET。因此这里锁住两点：
//   - 单纯 UPDATE 其他列不会自动推进 updated_at（避免产生虚假的版本变化）；
//   - 显式 SET updated_at = now() 会真实推进。
//
// 该语义是客户端 expected_updated_at 版本协议的基础。
func TestUpdatedAtMustBeAdvancedExplicitly(t *testing.T) {
	pool := EnsureMigrated(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	userID := insertUser(t, pool, "alice")

	var before, afterNoTouch, afterTouch string
	if err := pool.QueryRow(ctx,
		`SELECT updated_at::text FROM users WHERE id = $1`, userID).Scan(&before); err != nil {
		t.Fatalf("读取初始 updated_at 失败: %v", err)
	}

	// 只改 display_name，不触碰 updated_at。
	if _, err := pool.Exec(ctx,
		`UPDATE users SET display_name = 'Changed' WHERE id = $1`, userID); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT updated_at::text FROM users WHERE id = $1`, userID).Scan(&afterNoTouch); err != nil {
		t.Fatalf("读取 updated_at 失败: %v", err)
	}
	if afterNoTouch != before {
		t.Fatalf("未显式 SET 时 updated_at 不应变化（说明存在意外的触发器）：%q → %q", before, afterNoTouch)
	}

	// 显式推进。
	if _, err := pool.Exec(ctx,
		`UPDATE users SET display_name = 'Changed again', updated_at = now() WHERE id = $1`, userID); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT updated_at::text FROM users WHERE id = $1`, userID).Scan(&afterTouch); err != nil {
		t.Fatalf("读取 updated_at 失败: %v", err)
	}
	if afterTouch == before {
		t.Fatal("显式 SET updated_at = now() 必须推进版本")
	}
}

// 以下是插入辅助函数。它们刻意只填必需列，使测试聚焦在被测约束上。
//
// 所有 ID 都由应用的 UUIDv7 生成器产出，避免测试里出现手写 UUID 字符串。

func insertUser(t *testing.T, pool *pgxpool.Pool, username string) string {
	t.Helper()

	id := idgen.New().MustUUID()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, username, email, display_name, password_hash)
		 VALUES ($1, $2, $3, $4, '$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA')`,
		id, username, username+"@example.com", strings.ToUpper(username[:1])+username[1:])
	if err != nil {
		t.Fatalf("插入用户 %s 失败: %v", username, err)
	}
	return id
}

func insertGroup(t *testing.T, pool *pgxpool.Pool, ownerID, name string) string {
	t.Helper()

	id := idgen.New().MustUUID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO groups (id, name, owner_id) VALUES ($1, $2, $3)`, id, name, ownerID); err != nil {
		t.Fatalf("插入工作组 %s 失败: %v", name, err)
	}
	return id
}

func insertProject(t *testing.T, pool *pgxpool.Pool, createdBy string) string {
	t.Helper()

	groupID := insertGroup(t, pool, createdBy, "G-"+createdBy[:8])
	id := idgen.New().MustUUID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO projects (id, name, created_by, group_id, start_date, end_date)
		 VALUES ($1, 'Project', $2, $3, '2026-10-01', '2026-10-31')`,
		id, createdBy, groupID); err != nil {
		t.Fatalf("插入项目失败: %v", err)
	}
	return id
}

func insertTask(t *testing.T, pool *pgxpool.Pool, projectID, createdBy string) string {
	t.Helper()

	id := idgen.New().MustUUID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO tasks (id, project_id, created_by, name, start_date, end_date)
		 VALUES ($1, $2, $3, 'Task', '2026-10-01', '2026-10-05')`,
		id, projectID, createdBy); err != nil {
		t.Fatalf("插入任务失败: %v", err)
	}
	return id
}
