// Package testsupport 为集成测试提供真实 PostgreSQL / Redis 的装配与清理工具。
//
// 设计立场（backend/docs/B01-Go服务端架构设计.md 8）：
// 早期实现的 45 个单元测试全部通过，却没有发现"所有更新/删除端点 500"，
// 因为测试用替身替换掉了真正出错的那一层。因此 Go 版本把集成测试作为常规测试的一部分：
// 每一条 SQL 查询、每一个数据库约束都必须在真实数据库上被验证过。
//
// 降级策略：依赖不可达时 t.Skip 并说明原因，使 `go test ./...` 在无 Docker 的机器上
// 仍然全部通过，而不是报出一堆与代码无关的失败。
package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/myonku/project-builder/internal/app/attachments"
	"github.com/myonku/project-builder/internal/app/audit"
	"github.com/myonku/project-builder/internal/app/auth"
	"github.com/myonku/project-builder/internal/app/groups"
	"github.com/myonku/project-builder/internal/app/planviews"
	"github.com/myonku/project-builder/internal/app/projects"
	"github.com/myonku/project-builder/internal/app/reminders"
	"github.com/myonku/project-builder/internal/app/tasks"
	"github.com/myonku/project-builder/internal/app/users"
	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/database"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
	"github.com/myonku/project-builder/internal/platform/storage"
)

// 默认连接参数与 docker/compose.test.yml 保持一致。
//
// 复用的是 早期实现已在跑的测试依赖栈（PostgreSQL 16 / Redis 7 / Mailpit），
// 因此 Go 版本从第一阶段就具备真实依赖验证能力。
const (
	defaultDatabaseDSN = "postgres://project_builder_test:project_builder_test_password@127.0.0.1:15432/project_builder_test?sslmode=disable"
	defaultRedisAddr   = "127.0.0.1:16379"
	defaultRedisPass   = "project_builder_test_redis_password"

	// EnvDatabaseDSN / EnvRedisAddr / EnvRedisPassword 用于覆盖默认值。
	EnvDatabaseDSN   = "TEST_DATABASE_DSN"
	EnvRedisAddr     = "TEST_REDIS_ADDR"
	EnvRedisPassword = "TEST_REDIS_PASSWORD"
)

// DatabaseDSN 返回集成测试使用的连接串。
func DatabaseDSN() string {
	if dsn := os.Getenv(EnvDatabaseDSN); dsn != "" {
		return dsn
	}
	return defaultDatabaseDSN
}

// RedisAddr 返回集成测试使用的 Redis 地址与密码。
func RedisAddr() (string, string) {
	addr := defaultRedisAddr
	if value := os.Getenv(EnvRedisAddr); value != "" {
		addr = value
	}
	password := defaultRedisPass
	if value := os.Getenv(EnvRedisPassword); value != "" {
		password = value
	}
	return addr, password
}

// sharedState 把使用同一套 PostgreSQL / Redis 的集成测试串行化。
//
// 这是刻意的设计：各用例共享同一个物理数据库，TruncateAll 会清掉并发用例的数据。
// 因此集成测试**不得**使用 t.Parallel()——否则会出现"单独跑通过、一起跑失败"的
// 假故障（实测确认：并发用例互相清空数据，注册返回 409 或查不到刚写入的用户）。
//
// 代价是集成测试串行执行；收益是结果确定，失败一定指向真实缺陷。
var sharedState sync.Mutex

// advisoryLockKey 是集成测试使用的 PostgreSQL 会话级咨询锁标识。
//
// 用途：`go test ./...` 默认为每个包启动独立的测试二进制并并行执行。
// 进程内的 sync.Mutex 无法跨进程生效，因此"auth 包"与"testsupport 包"仍会互相
// 清空数据。这里用一个咨询锁把整个测试运行串行化，从根上消除该类假故障。
const advisoryLockKey int64 = 0x70726F6A65637431 // "project1"

// lockConn 是持有咨询锁的会话。必须保持打开，否则锁会在会话结束时释放。
var (
	lockOnce sync.Once
	lockConn *pgx.Conn
)

// HoldDatabaseLock 获取跨进程的数据库咨询锁，直到测试进程退出。
//
// 应在集成测试包的 TestMain 中调用一次。依赖不可达时不阻塞，仅记录并放行。
func HoldDatabaseLock() {
	lockOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := pgx.Connect(ctx, DatabaseDSN())
		if err != nil {
			// 依赖不可达：让各用例自行 skip，不要在这里失败。
			return
		}
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
			_ = conn.Close(ctx)
			return
		}
		lockConn = conn
	})
}

// ReleaseDatabaseLock 释放咨询锁。通常在 TestMain 退出前调用。
func ReleaseDatabaseLock() {
	if lockConn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockKey); err != nil {
		// 释放失败会随会话关闭自动回收，不需要让测试失败。
		_ = err
	}
	_ = lockConn.Close(ctx)
	lockConn = nil
}

// Logger 返回丢弃输出的日志器，避免测试噪声。
func Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// EnsureMigrated 把数据库迁移到最新版本，并返回连接池。
//
// 这是所有数据库集成测试的入口：既验证"空库可迁移"，也为后续断言提供干净 schema。
//
// 自愈行为：如果检测到 schema 处于脏状态（上一次迁移被中断，例如测试进程被强杀），
// 会先重建 public schema 再重新迁移。否则一次中断会让之后所有集成测试永久失败，
// 这是非常影响开发体验的假故障。
func EnsureMigrated(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := DatabaseDSN()

	// 先确认依赖可达，不可达则跳过而不是失败。
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelProbe()

	probe, err := pgxpool.New(probeCtx, dsn)
	if err != nil {
		t.Skipf("跳过集成测试：无法创建 PostgreSQL 连接池（%v）。请启动 docker compose -f docker/compose.test.yml up -d", err)
	}
	if err := probe.Ping(probeCtx); err != nil {
		probe.Close()
		t.Skipf("跳过集成测试：PostgreSQL 不可达（%v）。请启动 docker compose -f docker/compose.test.yml up -d", err)
	}
	probe.Close()

	if isDirty(t) {
		t.Log("检测到 schema 处于脏状态，重建 public schema 后重新迁移")
		resetSchema(t)
	}

	if err := migrateUp(t); err != nil {
		t.Fatalf("迁移到最新版本失败: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("创建连接池失败: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// migrateUp 执行一次迁移到最新版本。
func migrateUp(t *testing.T) error {
	t.Helper()

	migrator, err := database.OpenMigrator(DatabaseDSN(), Logger())
	if err != nil {
		return err
	}
	defer migrator.Close()

	return migrator.Up()
}

// isDirty 报告 schema_migrations 是否处于脏状态。
// 迁移表不存在（空库）时返回 false。
func isDirty(t *testing.T) bool {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), DatabaseDSN())
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var dirty bool
	err = conn.QueryRow(context.Background(),
		`SELECT dirty FROM schema_migrations LIMIT 1`).Scan(&dirty)
	if err != nil {
		// 表不存在或没有记录都表示"不需要修复"。
		return false
	}
	return dirty
}

// resetSchema 重建 public schema，用于从脏状态或残留数据中恢复。
//
// 这是破坏性操作，但它只作用于集成测试数据库，且仅在检测到脏状态时被调用。
func resetSchema(t *testing.T) {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), DatabaseDSN())
	if err != nil {
		t.Fatalf("连接数据库以重置 schema 失败: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	for _, statement := range []string{
		`DROP SCHEMA IF EXISTS public CASCADE`,
		`CREATE SCHEMA public`,
	} {
		if _, err := conn.Exec(context.Background(), statement); err != nil {
			t.Fatalf("执行 %q 失败: %v", statement, err)
		}
	}
}

// TruncateAll 清空全部业务表。
//
// 使用 TRUNCATE ... CASCADE 并重置迁移表之外的状态，
// 保证每个测试从确定状态开始，不受其他测试残留影响。
func TruncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	// activity_logs 也显式列出：它虽然会被 users 的 CASCADE 一起清掉，
	// 但"审计表从空开始"是每个用例的硬前提，不该依赖级联的传递性。
	const query = `TRUNCATE TABLE
		activity_logs, idempotency_keys, task_members, tasks, project_members, projects,
		group_join_requests, group_members, groups, users
		RESTART IDENTITY CASCADE`

	if _, err := pool.Exec(context.Background(), query); err != nil {
		t.Fatalf("清空业务表失败: %v", err)
	}
}

// OpenSQL 打开一个 database/sql 连接，用于需要 DDL / 多语句的断言。
func OpenSQL(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("pgx", DatabaseDSN())
	if err != nil {
		t.Fatalf("打开 database/sql 连接失败: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skipf("跳过集成测试：PostgreSQL 不可达（%v）", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Redis 封装集成测试用的 Redis 客户端。
type Redis struct {
	Client *redis.Client
}

// OpenRedis 连接测试 Redis。不可达时跳过测试。
func OpenRedis(t *testing.T) *Redis {
	t.Helper()

	addr, password := RedisAddr()
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       15, // 使用独立库，避免污染其他测试或本地开发数据
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("跳过集成测试：Redis 不可达（%v）。请启动 docker compose -f docker/compose.test.yml up -d", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return &Redis{Client: client}
}

// FlushSessions 清空会话键。
func (r *Redis) FlushSessions(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	iter := r.Client.Scan(ctx, 0, "pb:session:*", 100).Iterator()
	for iter.Next(ctx) {
		if err := r.Client.Del(ctx, iter.Val()).Err(); err != nil {
			t.Fatalf("清理会话键失败: %v", err)
		}
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("扫描会话键失败: %v", err)
	}
}

// TestConfig 返回集成测试使用的服务配置。
//
// 与生产默认值的差异只有 Argon2 强度与超时，其余（含 Cookie 名称、TTL）保持一致。
func TestConfig() config.Config {
	cfg := config.Config{
		APIVersion: "1",
		Env:        config.EnvTest,
		Server: config.ServerConfig{
			Host: "127.0.0.1", Port: 8080,
			ReadTimeoutSeconds: 15, WriteTimeoutSeconds: 30, ShutdownTimeoutSeconds: 10,
			CORSAllowedOrigins: []string{"http://127.0.0.1:5173"},
		},
		Database: config.DatabaseConfig{
			Host: "127.0.0.1", Port: 15432, Name: "project_builder_test",
			User: "project_builder_test", Password: "project_builder_test_password",
			SSLMode: "disable", MaxConns: 5, MinConns: 1,
			MaxConnLifetimeSeconds: 300, ConnectTimeoutSeconds: 5,
		},
		Redis: config.RedisConfig{
			Enabled: true, Host: "127.0.0.1", Port: 16379, DB: 15,
			Password: defaultRedisPass, DialTimeoutSeconds: 3,
		},
		Mongo: config.MongoConfig{
			URI:                           "mongodb://127.0.0.1:27018",
			Database:                      "project_builder_test",
			ConnectTimeoutSeconds:         5,
			ServerSelectionTimeoutSeconds: 3,
			MaxPoolSize:                   10,
		},
		Storage: config.StorageConfig{
			Backend: "local",
			// 附件写入包级临时目录：测试不应污染仓库工作区，
			// 也不能让上一次运行的残留影响本次断言。
			LocalRoot:         testUploadRoot(),
			GridFSBucket:      "attachments",
			AvatarBucket:      "avatars",
			AvatarMaxFileSize: 2 << 20,
			MaxFileSize:       testMaxFileSize,
			MaxFilesPerUser:   testMaxFilesPerUser,
			// 默认不限制扩展名（与配置项语义一致：空数组 = 不限制）。
			// 需要验证白名单的用例自己构造带白名单的服务实例。
			AllowedExtensions: nil,
		},
		Security: config.SecurityConfig{
			SessionSecret:     "integration-test-session-secret-value",
			SessionTTLSeconds: 3600,
			PasswordMinLength: 8,
			SecureCookies:     false,
			SessionCookieName: "pb_session_test",
			// 既有测试用 /auth/register 准备账号；默认关闭审核以保持原行为。
			// 注册审核链路由 NewHarnessWithOptions(RegistrationApproval: true) 的测试覆盖。
			RegistrationRequiresApproval: false,
		},
		Scheduler: config.SchedulerConfig{Enabled: false, IntervalSeconds: 60, ReminderBatchSize: 100, MaxRetries: 1},
	}
	cfg.Database.URL = DatabaseDSN()
	return cfg
}

// TestArgon2Params 返回低成本 Argon2 参数。
//
// 强度由生产默认值保证；集成测试要验证的是"摘要能被真实存取与校验"，
// 而不是花 3 秒/次去算参数强度。
func TestArgon2Params() platform.Argon2Params {
	return platform.Argon2Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}
}

// Harness 是一套完整的真实依赖测试装配。
type Harness struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Redis  *Redis
	Clock  *clock.Fixed
	IDs    *idgen.Generator
	// Users 是 auth 模块的用户仓储（注册与登录路径使用）。
	Users  *auth.Repository
	Hasher *platform.PasswordHasher

	// 各域服务，供测试直接调用（绕过 HTTP）或在断言中复用。
	Auth      *auth.Service
	Profiles  *users.Service
	Groups    *groups.Service
	Projects  *projects.Service
	Tasks     *tasks.Service
	PlanViews *planviews.Service
	// Attachments 是附件服务，供需要直接断言存储状态的用例使用。
	Attachments *attachments.Service
	// Storage 是附件对象存储（测试用它验证"彻底删除后字节是否真的没了"）。
	Storage storage.Store
	// Reminders 是提醒服务，供需要直接调用生成/派发的用例使用。
	Reminders *reminders.Service
	// Mail 是假发送器，供断言"邮件内容与收件人"使用。
	Mail *FakeSender
	// Audit 是审计服务：写入方由各业务模块在事务里调用，查询端点只有管理员可见。
	Audit *audit.Service

	// AuthHandler 同时是 ActorLoader，供调试与断言使用。
	AuthHandler *auth.Handler

	Router *gin.Engine
	Logger *slog.Logger
}

// HarnessOptions 是测试装配的可选差异。
type HarnessOptions struct {
	// RegistrationApproval 为真时启用"注册申请 + 管理员审核"链路；
	// 默认 false，保持既有测试用 /auth/register 直接创建 active 用户的行为。
	RegistrationApproval bool
}

// NewHarness 装配真实 PostgreSQL + Redis 的测试环境。
//
// 已迁移到最新 schema，已清空业务表与会话键，因此每个用例从空库开始。
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	return NewHarnessWithOptions(t, HarnessOptions{})
}

// NewHarnessWithOptions 与 NewHarness 相同，但允许测试显式启用注册审核等差异。
func NewHarnessWithOptions(t *testing.T, opts HarnessOptions) *Harness {
	t.Helper()

	// 串行化：见 sharedState 的说明。
	sharedState.Lock()
	t.Cleanup(sharedState.Unlock)

	pool := EnsureMigrated(t)
	TruncateAll(t, pool)
	redisHarness := OpenRedis(t)
	redisHarness.FlushSessions(t)

	logger := Logger()
	cfg := TestConfig()
	cfg.Security.RegistrationRequiresApproval = opts.RegistrationApproval
	clk := clock.NewFixed(time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC))
	ids := idgen.NewTimeOrdered(clk.Now)
	hasher := platform.NewPasswordHasherWith(TestArgon2Params())

	authUsers := auth.NewRepository(pool)
	store := auth.NewRedisSessionStore(redisHarness.Client, true, clk)

	// 审计：与 cmd/api 的装配保持一致——写入方注入到各业务模块的事务里，
	// 查询方只有系统管理员。测试必须走与生产同一条路径，否则"审计有没有写进去"
	// 就不是在验证生产行为。
	auditService := audit.NewService(audit.Options{
		Store:  audit.NewRepository(pool),
		IDs:    ids,
		Logger: logger,
	})

	fakeSender := &FakeSender{}
	cfg.Security.PasswordResetEnabled = true
	cfg.Security.PasswordResetTokenTTLSeconds = 1800
	cfg.Security.PasswordResetCooldownSeconds = 604800
	cfg.Security.PasswordResetWindowSeconds = 604800
	cfg.Security.PasswordResetMaxRequests = 5
	cfg.Mail.Enabled = true
	cfg.Mail.PasswordResetURLTemplate = "http://localhost:5173/reset-password?token={token}"
	authService := auth.NewService(auth.Options{
		Users:  authUsers,
		Resets: authUsers, Sender: fakeSender, Mail: cfg.Mail,
		Registrations: authUsers,
		Sessions:      store,
		Hasher:        hasher,
		IDs:           ids,
		Clock:         clk,
		Logger:        logger,
		Security:      cfg.Security,
		Audit:         auditService,
	})
	authHandler := auth.NewHandler(authService, logger)

	groupsService := groups.NewService(groups.Options{
		Store:  groups.NewRepository(pool),
		IDs:    ids,
		Clock:  clk,
		Logger: logger,
		Audit:  auditService,
	})
	// usersService 兼作 projects 的时区来源：与 cmd/api 的装配保持一致，
	// 否则测试验证的就不是生产行为（见 testsupport/adapter.go 的同类说明）。
	// 头像与附件共用同一个 local 根目录（与生产 local 模式一致）。
	avatarStore, err := storage.NewLocal(cfg.Storage.LocalRoot)
	if err != nil {
		t.Fatalf("构造头像存储失败: %v", err)
	}
	usersService := users.NewService(users.Options{
		Store:             users.NewRepository(pool),
		Accounts:          authService,
		Logger:            logger,
		Audit:             auditService,
		Storage:           avatarStore,
		IDs:               ids,
		AvatarMaxFileSize: cfg.Storage.AvatarMaxFileSize,
	})
	// 提醒服务：注入**假发送器**，于是全部提醒逻辑都能在没有 SMTP 的情况下验证。

	remindersService := reminders.NewService(reminders.Options{
		Store:           reminders.NewRepository(pool),
		Sender:          fakeSender,
		IDs:             ids,
		Clock:           clk,
		Logger:          logger,
		TaskURLTemplate: "https://example.test/projects/{project_id}?taskId={task_id}",
		BatchSize:       100,
		Audit:           auditService,
	})
	projectsService := projects.NewService(projects.Options{
		Store:     projects.NewRepository(pool),
		Timezones: usersService,
		IDs:       ids,
		Clock:     clk,
		Logger:    logger,
		// 与 cmd/api 一致：测试必须验证"生产装配下审计真的写进去了"。
		Audit: auditService,
	})
	tasksService := tasks.NewService(tasks.Options{
		Store:     tasks.NewRepository(pool),
		Projects:  ProjectAccessAdapter{Service: projectsService},
		Reminders: ReminderRecorderAdapter{Service: remindersService, Logger: logger},
		IDs:       ids,
		Clock:     clk,
		Logger:    logger,
		Audit:     auditService,
	})
	planViewsService := planviews.NewService(planviews.Options{
		Store:     planviews.NewRepository(pool),
		Timezones: usersService,
		Clock:     clk,
		Logger:    logger,
	})

	attachmentStore, err := storage.NewLocal(cfg.Storage.LocalRoot)
	if err != nil {
		t.Fatalf("构造附件存储失败: %v", err)
	}
	attachmentsService := attachments.NewService(attachments.Options{
		Store:    attachments.NewRepository(pool),
		Storage:  attachmentStore,
		Projects: AttachmentProjectAccess{Service: projectsService},
		Tasks:    AttachmentTaskAccess{Service: tasksService},
		IDs:      ids,
		Clock:    clk,
		Logger:   logger,
		Config:   cfg.Storage,
		Audit:    auditService,
	})

	engine := httpapi.Router(httpapi.Dependencies{
		Config: cfg,
		Logger: logger,
		Clock:  clk,
		IDs:    ids,
		Health: httpapi.Health{
			PostgresPing: func() error { return pool.Ping(context.Background()) },
			RedisPing:    func() error { return redisHarness.Client.Ping(context.Background()).Err() },
		},
		Modules: []httpapi.Registrar{
			authHandler,
			users.NewHandler(usersService, authHandler, logger),
			groups.NewHandler(groupsService, authHandler, logger),
			projects.NewHandler(projectsService, authHandler, logger),
			tasks.NewHandler(tasksService, authHandler, logger),
			planviews.NewHandler(planViewsService, authHandler, logger),
			attachments.NewHandler(attachmentsService, authHandler),
			reminders.NewHandler(remindersService, authHandler),
			audit.NewHandler(auditService, authHandler),
		},
	})

	return &Harness{
		Config:      cfg,
		Pool:        pool,
		Redis:       redisHarness,
		Clock:       clk,
		IDs:         ids,
		Users:       authUsers,
		Hasher:      hasher,
		Auth:        authService,
		Profiles:    usersService,
		Groups:      groupsService,
		Projects:    projectsService,
		Tasks:       tasksService,
		PlanViews:   planViewsService,
		Attachments: attachmentsService,
		Reminders:   remindersService,
		Mail:        fakeSender,
		Audit:       auditService,
		Storage:     attachmentStore,
		AuthHandler: authHandler,
		Router:      engine,
		Logger:      logger,
	}
}

// 附件相关的测试常量。
//
// 配额刻意取小值：验证"达到上限被拒绝"只需要两次上传，
// 用 1000（生产默认）会让用例变得又慢又难读。
const (
	testMaxFileSize     int64 = 1 << 16 // 64 KiB，足以覆盖正常与超限两种情况
	testMaxFilesPerUser       = 3
)

// testUploadRoot 返回本次测试进程使用的附件根目录。
//
// 用进程级临时目录而不是仓库内路径：测试不得污染工作区，
// 也不能让上一次运行的残留影响本次断言（附件是少数会真正落盘的测试数据）。
var testUploadRootOnce = sync.OnceValue(func() string {
	root, err := os.MkdirTemp("", "project-builder-uploads-*")
	if err != nil {
		panic("创建测试附件目录失败: " + err.Error())
	}
	return root
})

func testUploadRoot() string { return testUploadRootOnce() }

// RepoRoot 返回仓库根目录，供需要读取迁移文件的测试使用。
func RepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录失败: %v", err)
	}
	// 向上找到含 go.mod 的目录。
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("未能定位仓库根目录（从 %s 向上查找）", dir)
		}
		dir = parent
	}
}

// URL 拼接版本化路径。
func URL(path string) string { return "/api/v1" + path }

// Describe 返回用于失败信息的简短标识。
func Describe(value any) string { return fmt.Sprintf("%v", value) }
