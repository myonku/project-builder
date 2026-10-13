// Package database 负责版本化 SQL 迁移的加载与执行。
//
// 迁移文件以 golang-migrate 风格成对存在于 migrations/ 目录，并通过 embed 编译进二进制，
// 使部署物自包含、无需额外挂载迁移目录。
package database

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// 注册 database/sql 的 pgx 驱动（名称为 "pgx"）。
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsDir 是 embed 中的迁移目录名。
const migrationsDir = "migrations"

// Migrator 封装迁移执行。
type Migrator struct {
	m       *migrate.Migrate
	db      *sql.DB
	logger  *slog.Logger
	release func()
}

// OpenMigrator 基于 PostgreSQL 连接串打开迁移器。
//
// dsn 必须是 pgx 可解析的连接串；调用方需确保已使用 config.DatabaseConfig.DSN() 做过转义。
func OpenMigrator(dsn string, logger *slog.Logger) (*Migrator, error) {
	source, err := iofs.New(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("加载内嵌迁移文件失败: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开迁移用数据库连接失败: %w", err)
	}

	// golang-migrate 在 PostgreSQL 上使用会话级咨询锁串行化迁移，
	// 因此限制为单连接，避免连接池把锁与后续语句分配到不同会话。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("迁移用数据库连接不可用: %w", err)
	}

	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化迁移驱动失败: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化迁移器失败: %w", err)
	}
	m.Log = &migrateLogger{logger: logger}

	return &Migrator{
		m:      m,
		db:     db,
		logger: logger,
		release: func() {
			// 先关闭迁移器（释放 source 与 driver），再关闭底层连接。
			if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
				logger.Debug("关闭迁移器时出现问题", slog.Any("source_err", srcErr), slog.Any("database_err", dbErr))
			}
			_ = db.Close()
		},
	}, nil
}

// Up 应用全部未执行的迁移。
func (mg *Migrator) Up() error {
	err := mg.m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行迁移失败: %w", err)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		mg.logger.Info("schema 已是最新，无需迁移")
	}
	return nil
}

// Steps 相对当前版本前进（正数）或后退（负数）若干步。
func (mg *Migrator) Steps(n int) error {
	err := mg.m.Steps(n)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行迁移步进失败: %w", err)
	}
	return nil
}

// Down 回退全部迁移（清空 schema）。
func (mg *Migrator) Down() error {
	err := mg.m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("回退迁移失败: %w", err)
	}
	return nil
}

// Version 返回当前迁移版本与是否处于脏状态。
//
// 脏状态意味着上一次迁移中途失败，必须人工介入，绝不能继续自动迁移。
func (mg *Migrator) Version() (version uint, dirty bool, err error) {
	version, dirty, err = mg.m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, fmt.Errorf("读取迁移版本失败: %w", err)
	}
	return version, dirty, nil
}

// Force 强制设置版本并清除脏标记。仅用于人工修复，调用方必须记录原因。
func (mg *Migrator) Force(version int) error {
	if err := mg.m.Force(version); err != nil {
		return fmt.Errorf("强制设置迁移版本失败: %w", err)
	}
	return nil
}

// Close 释放迁移器资源。
func (mg *Migrator) Close() {
	if mg.release != nil {
		mg.release()
		mg.release = nil
	}
}

// migrateLogger 把 golang-migrate 的日志接入 slog，并过滤掉过于嘈杂的条目。
type migrateLogger struct {
	logger *slog.Logger
}

// Printf 实现 migrate.Logger。
func (l *migrateLogger) Printf(format string, v ...any) {
	l.logger.Info(fmt.Sprintf(format, v...))
}

// Verbose 报告是否需要详细日志（迁移过程本身足够重要，保持开启）。
func (l *migrateLogger) Verbose() bool { return true }
