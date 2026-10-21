// Command api 是服务端唯一入口：读取配置、装配依赖、启动 HTTP、优雅关闭。
//
// 装配策略（backend/docs/B01-Go服务端架构设计.md 4.4）：显式构造每一个依赖，
// 不使用反射式依赖注入，也不引入工厂层。所有外部资源在启动时创建一次，
// 通过结构体注入到各业务模块。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
	"github.com/myonku/project-builder/internal/platform/mail"
	"github.com/myonku/project-builder/internal/platform/storage"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func main() {
	if err := run(); err != nil {
		// 启动失败必须快速失败并给出脱敏后的原因（全局基线约定 7.4）。
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger()
	slog.SetDefault(logger)

	cfg, err := config.Load(os.Getenv("APP_CONFIG_FILE"), os.Getenv("APP_ENV_FILE"))
	if err != nil {
		return err
	}

	// 启动摘要：只输出脱敏后的字段与"是否已配置"，绝不输出敏感值。
	logger.Info("配置已加载", cfg.Summary()...)

	// 根 context：收到 SIGINT/SIGTERM 时取消，用于中断启动过程与后台任务。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	clk := clock.NewSystem()
	ids := idgen.NewTimeOrdered(clk.Now)

	postgres, err := platform.NewPostgres(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer postgres.Close()

	redisClient, err := platform.NewRedis(ctx, cfg.Redis, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			logger.Warn("关闭 Redis 客户端失败", slog.Any("cause", closeErr))
		}
	}()

	// Redis 客户端的 *redis.Client 在未启用时为 nil，会话存储据此报告不可用，
	// 而不是让整个应用启动失败（历史缺陷对照：早期实现此处在关闭 Redis 时全站 500）。
	var redisRaw = redisClient.Client

	sessionStore := auth.NewRedisSessionStore(redisRaw, redisClient.Enabled, clk)
	if !sessionStore.Available() {
		logger.Warn("会话后端不可用：认证相关端点将返回错误，服务其余部分仍可运行")
	}

	// MongoDB 只承载附件/头像字节；local 模式完全不建立连接，
	// 因此默认部署不要求 mongod 存在。gridfs 模式启动即检查连通性。
	var mongoClient *mongo.Client
	if strings.EqualFold(strings.TrimSpace(cfg.Storage.Backend), "gridfs") {
		mongoStore, err := platform.NewMongo(ctx, cfg.Mongo, logger)
		if err != nil {
			return err
		}
		mongoClient = mongoStore.Client
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if closeErr := mongoStore.Close(closeCtx); closeErr != nil {
				logger.Warn("关闭 MongoDB 客户端失败", slog.Any("cause", closeErr))
			}
		}()
	} else {
		logger.Info("对象存储使用 local；MongoDB 不连接")
	}

	// 审计：写入方由各业务模块在自己的事务里调用（apperr.AuditWriter），
	// 查询方只有系统管理员（契约 4.10）。它不依赖任何业务模块，
	// 因此必须在业务模块之前构造并注入。
	auditService := audit.NewService(audit.Options{
		Store:  audit.NewRepository(postgres.Pool),
		IDs:    ids,
		Logger: logger,
	})

	// 对象存储适配器由装配点构造（业务模块不依赖具体存储实现）。
	// 附件与头像使用两个独立 Store：gridfs 模式对应两个 bucket，local 模式共用根目录。
	attachmentStore, err := storage.NewFromConfig(cfg.Storage, mongoClient, cfg.Mongo.Database)
	if err != nil {
		return err
	}
	avatarStore, err := storage.NewAvatarFromConfig(cfg.Storage, mongoClient, cfg.Mongo.Database)
	if err != nil {
		return err
	}

	authStore := auth.NewRepository(postgres.Pool)
	authService := auth.NewService(auth.Options{
		Users:         authStore,
		Registrations: authStore,
		Sessions:      sessionStore,
		Hasher:        platform.NewPasswordHasher(),
		IDs:           ids,
		Clock:         clk,
		Logger:        logger,
		Security:      cfg.Security,
		// 自助注册的操作记录：操作人就是新建的账号本身。
		Audit: auditService,
	})

	// authHandler 同时承担两个角色：注册自身端点，以及为其他模块解析当前用户。
	// 各业务模块只依赖 groups.ActorLoader 契约，不直接接触会话细节。
	authHandler := auth.NewHandler(authService, logger)

	// 系统初始化时录入引导管理员。
	//
	// 为什么必须在启动阶段做：全部管理员端点都要求调用者已是管理员，
	// 没有任何 HTTP 路径能造出第一个管理员。本系统面向内部小组，
	// 普通用户不需要管理员角色，因此不做分发，只在初始化时录入一个系统级后台账号。
	//
	// 该操作是幂等的：账号已存在时只确保角色，绝不覆盖密码。
	if cfg.Security.BootstrapAdminEnabled() {
		result, err := authService.EnsureAdministrator(ctx, auth.BootstrapAdminInput{
			Username:    cfg.Security.BootstrapAdminUsername,
			Email:       cfg.Security.BootstrapAdminEmail,
			Password:    cfg.Security.BootstrapAdminPassword,
			DisplayName: cfg.Security.BootstrapAdminDisplayName,
		})
		if err != nil {
			// 初始化失败必须快速失败：否则服务会在"没有可用管理员"的状态下运行，
			// 而操作者以为自己已经录入完毕。
			return fmt.Errorf("引导管理员初始化失败: %w", err)
		}
		if result.Disabled {
			logger.Warn("引导管理员账号处于停用状态，在启用之前无法登录")
		}
	}

	// 审计服务已在 auth 之前构造（见上）。

	groupsService := groups.NewService(groups.Options{
		Store:  groups.NewRepository(postgres.Pool),
		IDs:    ids,
		Clock:  clk,
		Logger: logger,
		Audit:  auditService,
	})

	// usersService 同时承担三个角色：注册自身端点、为其他模块提供
	// "按用户 ID 查时区"的能力（派生逾期字段按用户时区计算"今天"），
	// 以及复用 auth 的账号能力（管理员代建账号、重置密码）。
	// `*users.Service` 与 `*auth.Service` 分别直接满足 projects.TimezoneProvider 与
	// users.Accounts，因此装配点不需要写适配器：接口方法签名与实现完全一致。
	usersService := users.NewService(users.Options{
		Store:             users.NewRepository(postgres.Pool),
		Accounts:          authService,
		Logger:            logger,
		Audit:             auditService,
		Storage:           avatarStore,
		IDs:               ids,
		AvatarMaxFileSize: cfg.Storage.AvatarMaxFileSize,
	})

	// 邮件发送器：未启用时注入 Disabled（它返回错误而不是假装成功，
	// 于是"没配邮件"会如实记成 skipped 而不是 sent）。
	var mailSender mail.Sender = mail.Disabled{}
	if cfg.Mail.Enabled {
		smtpSender, smtpErr := mail.NewSMTP(mail.SMTPConfig{
			Host:              cfg.Mail.Host,
			Port:              cfg.Mail.Port,
			Username:          cfg.Mail.Username,
			Password:          cfg.Mail.Password,
			FromAddress:       cfg.Mail.FromAddress,
			FromName:          cfg.Mail.FromName,
			UseTLS:            cfg.Mail.UseTLS,
			ImplicitTLS:       cfg.Mail.ImplicitTLS,
			AuthMethod:        cfg.Mail.AuthMethod,
			OAuth2AccessToken: cfg.Mail.OAuth2AccessToken,
		})
		if smtpErr != nil {
			return smtpErr
		}
		mailSender = smtpSender
		logger.Info("邮件发送已启用", slog.String("host", cfg.Mail.Host), slog.Int("port", cfg.Mail.Port))
	} else {
		logger.Info("邮件发送未启用；提醒仍会生成并记录为 skipped")
	}

	remindersService := reminders.NewService(reminders.Options{
		Store:           reminders.NewRepository(postgres.Pool),
		Sender:          mailSender,
		IDs:             ids,
		Clock:           clk,
		Logger:          logger,
		TaskURLTemplate: cfg.Mail.TaskURLTemplate,
		BatchSize:       cfg.Scheduler.ReminderBatchSize,
		// 只有人工重试会写审计（后台派发没有操作人，见 reminders.auditWith）。
		Audit: auditService,
	})

	projectsService := projects.NewService(projects.Options{
		Store:     projects.NewRepository(postgres.Pool),
		Timezones: usersService,
		IDs:       ids,
		Clock:     clk,
		Logger:    logger,
		// 审计写入方：项目域的关键写操作在同事务里记一条（《A09》）。
		Audit: auditService,
	})

	tasksService := tasks.NewService(tasks.Options{
		Store: tasks.NewRepository(postgres.Pool),
		// 装配点负责把 projects 的项目记录适配为 tasks 所需的投影，
		// 这样两个业务模块之间不产生 import 依赖。
		Projects: projectsAccessor{service: projectsService},
		IDs:      ids,
		Clock:    clk,
		Logger:   logger,
		// 提醒的消费者接口：任务在事务提交后只插入一行提醒，
		// 发送交给后台派发循环（需求文档 §9：邮件发送不得阻塞用户操作）。
		Reminders: remindersRecorder{service: remindersService, logger: logger},
		Audit:     auditService,
	})

	// 附件：对象存储适配器已在上面构造（与头像分开）。
	attachmentsService := attachments.NewService(attachments.Options{
		Store:   attachments.NewRepository(postgres.Pool),
		Storage: attachmentStore,
		// 装配点把项目/任务的权限判定适配为附件模块所需的投影。
		Projects: attachmentsProjectAccess{service: projectsService},
		Tasks:    attachmentsTaskAccess{service: tasksService},
		IDs:      ids,
		Clock:    clk,
		Logger:   logger,
		Config:   cfg.Storage,
		Audit:    auditService,
	})

	// 计划视图是只读聚合模块：它不依赖 projects/tasks 模块，而是直接读同一批表，
	// 并调用 domain 的同一个权限函数做逐对象判定（见 planviews 包注释）。
	planViewsService := planviews.NewService(planviews.Options{
		Store:     planviews.NewRepository(postgres.Pool),
		Timezones: usersService,
		Clock:     clk,
		Logger:    logger,
	})

	engine := httpapi.Router(httpapi.Dependencies{
		Config: cfg,
		Logger: logger,
		Clock:  clk,
		IDs:    ids,
		Health: httpapi.Health{
			PostgresPing: func() error {
				pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return postgres.Ping(pingCtx)
			},
			RedisPing: func() error {
				if !redisClient.Enabled {
					return nil
				}
				pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return redisClient.Ping(pingCtx)
			},
			MongoPing: func() error {
				if mongoClient == nil {
					return nil
				}
				pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return mongoClient.Ping(pingCtx, nil)
			},
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

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout(),
		WriteTimeout: cfg.Server.WriteTimeout(),
		// IdleTimeout 略大于写超时，保证连接复用但不会长期挂住。
		IdleTimeout: cfg.Server.WriteTimeout() + 30*time.Second,
	}

	// 在独立 goroutine 中监听，主流程等待关闭信号。
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务已启动",
			slog.String("address", server.Addr),
			slog.String("api_prefix", "/api/v"+cfg.APIVersion),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("HTTP 服务异常退出: %w", err)
			return
		}
		serveErr <- nil
	}()

	// 提醒派发器：按配置周期生成并发送提醒。
	// 它与 HTTP 服务共享根 ctx，因此收到关闭信号时一起停下——
	// 半路被杀的派发器会留下"已经发出但状态没写回"的提醒，
	// 下一轮会重复发送（dedupe_key 只防重复**生成**，防不了重复发送）。
	dispatcher := reminders.NewDispatcher(remindersService,
		time.Duration(cfg.Scheduler.IntervalSeconds)*time.Second, logger)
	if cfg.Scheduler.Enabled {
		go dispatcher.Run(ctx)
	} else {
		logger.Info("提醒派发器未启用（scheduler.enabled = false）；提醒仍会由接口按需生成")
	}

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("收到关闭信号，开始优雅关闭")
	}

	// 优雅关闭：给在途请求一个明确的预算，超时后强制结束。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout())
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭超时: %w", err)
	}
	logger.Info("服务已关闭")
	return nil
}

// projectsAccessor 把 projects 模块的项目记录适配为 tasks 模块所需的投影。
//
// 存在的唯一理由：让两个业务模块互不依赖。适配逻辑本身只有字段搬运，
// 因此刻意保持极薄——如果这里出现业务判断，说明边界划错了。
type projectsAccessor struct {
	service *projects.Service
}

// Access 实现 tasks.ProjectAccess。
func (a projectsAccessor) Access(
	ctx context.Context, projectID, actorID string,
) (tasks.ProjectView, domain.Permission, error) {
	record, permission, err := a.service.AccessFor(ctx, projectID, actorID)
	if err != nil {
		return tasks.ProjectView{}, domain.PermNone, err
	}
	return tasks.ProjectView{
		ID:         record.ID,
		Status:     record.Status,
		Visibility: record.Visibility,
		ArchivedAt: record.ArchivedAt,
	}, permission, nil
}

// remindersRecorder 把 tasks 模块的提醒记录请求适配为 reminders 模块的调用。
//
// 错误只记录日志，**不让任务操作失败**：任务已经写库成功，
// 因为一封提醒没排上队就把它回滚，是把次要功能的失败放大成主要功能的失败。
type remindersRecorder struct {
	service *reminders.Service
	logger  *slog.Logger
}

// RecordAssignment 实现 tasks.ReminderRecorder：成员被加入任务后记一条提醒。
func (r remindersRecorder) RecordAssignment(ctx context.Context, taskID, userID string) {
	if r.service == nil {
		return
	}
	if err := r.service.RecordEvent(ctx, taskID, userID, domain.ReminderKindAssigned); err != nil {
		r.logger.Warn("记录新任务分配提醒失败",
			slog.String("task_id", taskID), slog.String("user_id", userID), slog.Any("cause", err))
	}
}

// RecordOwnerChange 实现 tasks.ReminderRecorder：任务持有者变更后记一条提醒。
func (r remindersRecorder) RecordOwnerChange(ctx context.Context, taskID, userID string) {
	if r.service == nil {
		return
	}
	if err := r.service.RecordEvent(ctx, taskID, userID, domain.ReminderKindOwnerChanged); err != nil {
		r.logger.Warn("记录持有者变更提醒失败",
			slog.String("task_id", taskID), slog.String("user_id", userID), slog.Any("cause", err))
	}
}

// attachmentsProjectAccess 把 projects 模块的项目记录适配为 attachments 所需的投影。
type attachmentsProjectAccess struct {
	service *projects.Service
}

// Access 实现 attachments.ProjectAccess。
func (a attachmentsProjectAccess) Access(
	ctx context.Context, projectID, actorID string,
) (attachments.ProjectInfo, domain.Permission, error) {
	record, permission, err := a.service.Access(ctx, projectID, actorID)
	if err != nil {
		return attachments.ProjectInfo{}, domain.PermNone, err
	}
	return attachments.ProjectInfo{
		ID: record.ID,
		// 归档项目只读，因此其附件也是只读的（与"归档项目下任务只读"同一条规则）。
		ReadOnly: record.ToDomain().IsReadOnly(),
	}, permission, nil
}

// attachmentsTaskAccess 把 tasks 模块的任务记录适配为 attachments 所需的投影。
type attachmentsTaskAccess struct {
	service *tasks.Service
}

// Access 实现 attachments.TaskAccess。
func (a attachmentsTaskAccess) Access(
	ctx context.Context, taskID, actorID string,
) (attachments.TaskInfo, domain.Permission, error) {
	record, permission, err := a.service.AccessForAttachment(ctx, taskID, actorID)
	if err != nil {
		return attachments.TaskInfo{}, domain.PermNone, err
	}
	return attachments.TaskInfo{ID: record.ID, ProjectID: record.ProjectID}, permission, nil
}

// newLogger 构造结构化日志器。//
// 时间统一由 slog 以 UTC 输出（全局基线约定 12）；
// 日志中不得写入密码、令牌、完整邮件内容或附件内容。
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			// 统一把时间改写为 UTC，避免不同机器的本地时区混进日志。
			if attr.Key == slog.TimeKey {
				if t, ok := attr.Value.Any().(time.Time); ok {
					return slog.Time(slog.TimeKey, t.UTC())
				}
			}
			return attr
		},
	})
	return slog.New(handler)
}
