// Command migrate 是数据库迁移入口。
//
// 用法：
//
//	migrate up                应用全部未执行的迁移
//	migrate down              回退全部迁移（清空 schema）
//	migrate steps <n>         相对当前版本前进/后退 n 步（n 可为负）
//	migrate version           输出当前版本与脏标记
//	migrate force <version>   强制设置版本并清除脏标记（人工修复用）
//
// 配置来源与 API 服务完全一致：代码默认值 < config.toml < .env < 进程环境变量。
// 迁移执行使用独立的 database/sql 连接（golang-migrate 需要会话级咨询锁），
// 不复用 API 服务的 pgxpool。
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/database"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "迁移失败: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少子命令；可用：up | down | steps <n> | version | force <version>")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(os.Getenv("APP_CONFIG_FILE"), os.Getenv("APP_ENV_FILE"))
	if err != nil {
		return err
	}

	// 迁移只需要数据库配置；显式打印脱敏后的目标库，避免"迁移到了错误的库"。
	logger.Info("迁移目标已解析",
		slog.String("env", cfg.Env),
		slog.String("config_file", cfg.ConfigFile),
		slog.String("database_dsn", cfg.Database.RedactedDSN()),
	)

	migrator, err := database.OpenMigrator(cfg.Database.DSN(), logger)
	if err != nil {
		return err
	}
	defer migrator.Close()

	switch command := args[0]; command {
	case "up":
		if err := migrator.Up(); err != nil {
			return err
		}
		return reportVersion(migrator, logger)

	case "down":
		if err := migrator.Down(); err != nil {
			return err
		}
		logger.Info("已回退全部迁移")
		return nil

	case "steps":
		if len(args) != 2 {
			return fmt.Errorf("steps 需要且仅需要一个整数参数")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("steps 参数 %q 不是合法整数", args[1])
		}
		if err := migrator.Steps(n); err != nil {
			return err
		}
		return reportVersion(migrator, logger)

	case "version":
		return reportVersion(migrator, logger)

	case "force":
		if len(args) != 2 {
			return fmt.Errorf("force 需要且仅需要一个整数版本参数")
		}
		version, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("force 参数 %q 不是合法整数", args[1])
		}
		if err := migrator.Force(version); err != nil {
			return err
		}
		logger.Warn("已强制设置迁移版本", slog.Int("version", version))
		return nil

	default:
		return fmt.Errorf("未知子命令 %q；可用：up | down | steps <n> | version | force <version>", command)
	}
}

// reportVersion 输出当前迁移版本，并在脏状态下返回错误。
//
// 脏状态意味着上一次迁移中途失败，必须人工介入后才能继续，
// 因此这里把它当作失败处理，避免 CI 在 schema 不确定的情况下继续跑测试。
func reportVersion(migrator *database.Migrator, logger *slog.Logger) error {
	version, dirty, err := migrator.Version()
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("schema 处于脏状态（version=%d），必须先用 force 修复", version)
	}
	logger.Info("当前迁移版本", slog.Uint64("version", uint64(version)))
	return nil
}
