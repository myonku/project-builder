// Command storage-migrate 在本地文件系统与 MongoDB GridFS 之间搬迁附件字节。
//
// 它**只搬字节并更新 attachments.storage_backend 账本**，不改任何业务字段、
// 不改变 stored_key，也不做双读/双写。用法：
//
//	storage-migrate copy     --from local  --to gridfs [--dry-run]
//	storage-migrate verify   --from local  --to gridfs
//	storage-migrate rollback --from gridfs --to local  [--dry-run]
//
// copy/rollback 可以重复执行：同一个 key 的 Put 是覆盖语义，账本只在成功后更新。
// 回滚不会删除 GridFS 中的旧对象（保留用于人工核对/再次迁移）；确认无误后
// 由运维按 B03 §7.4 清理 bucket。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/platform/storage"
)

type attachmentRow struct {
	ID      string
	Key     string
	Size    int64
	Backend string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "存储迁移失败: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	from := flags.String("from", "", "源后端：local | gridfs")
	to := flags.String("to", "", "目标后端：local | gridfs")
	dryRun := flags.Bool("dry-run", false, "只打印将要处理的行，不写入/不更新账本")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	*from = strings.ToLower(strings.TrimSpace(*from))
	*to = strings.ToLower(strings.TrimSpace(*to))
	if *from == "" || *to == "" {
		return usageError()
	}
	if *from == *to {
		return fmt.Errorf("--from 与 --to 不能相同（%s）", *from)
	}
	switch command {
	case "copy", "rollback", "verify":
	default:
		return usageError()
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(os.Getenv("APP_CONFIG_FILE"), os.Getenv("APP_ENV_FILE"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	postgres, err := platform.NewPostgres(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer postgres.Close()

	var mongoClient *mongo.Client
	if *from == "gridfs" || *to == "gridfs" {
		if strings.TrimSpace(cfg.Mongo.URI) == "" || strings.TrimSpace(cfg.Mongo.Database) == "" {
			return errors.New("使用 gridfs 时必须配置 mongo.uri 与 mongo.database")
		}
		mongoStore, err := platform.NewMongo(ctx, cfg.Mongo, logger)
		if err != nil {
			return err
		}
		mongoClient = mongoStore.Client
		defer func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer closeCancel()
			_ = mongoStore.Close(closeCtx)
		}()
	}

	source, err := newStore(&cfg, *from, mongoClient)
	if err != nil {
		return fmt.Errorf("构造源存储失败: %w", err)
	}
	target, err := newStore(&cfg, *to, mongoClient)
	if err != nil {
		return fmt.Errorf("构造目标存储失败: %w", err)
	}

	if command == "verify" {
		return verify(ctx, postgres.Pool, source, target, *from, *to, logger)
	}
	return copyRows(ctx, postgres.Pool, source, target, *from, *to, *dryRun, logger)
}

func usageError() error {
	return errors.New("用法：storage-migrate copy|rollback|verify --from local|gridfs --to local|gridfs [--dry-run]")
}

func newStore(cfg *config.Config, backend string, mongoClient *mongo.Client) (storage.Store, error) {
	switch backend {
	case "local":
		return storage.NewLocal(cfg.Storage.LocalRoot)
	case "gridfs":
		bucket := strings.TrimSpace(cfg.Storage.GridFSBucket)
		if bucket == "" {
			bucket = "attachments"
		}
		return storage.NewGridFS(mongoClient, cfg.Mongo.Database, bucket)
	default:
		return nil, fmt.Errorf("未知后端 %q", backend)
	}
}

func copyRows(
	ctx context.Context,
	pool *pgxpool.Pool,
	source, target storage.Store,
	from, to string,
	dryRun bool,
	logger *slog.Logger,
) error {
	rows, err := queryRows(ctx, pool, from)
	if err != nil {
		return err
	}
	logger.Info("开始搬迁附件字节",
		slog.String("from", from), slog.String("to", to),
		slog.Int("rows", len(rows)), slog.Bool("dry_run", dryRun))
	if dryRun {
		for _, row := range rows {
			logger.Info("将搬迁", slog.String("attachment_id", row.ID), slog.String("key", row.Key))
		}
		return nil
	}

	copied := 0
	for _, row := range rows {
		if err := copyOne(ctx, source, target, row); err != nil {
			return fmt.Errorf("搬迁 %s 失败: %w", row.ID, err)
		}
		tag, err := pool.Exec(ctx,
			`UPDATE attachments SET storage_backend = $1, updated_at = now()
			  WHERE id = $2 AND storage_backend = $3`, to, row.ID, from)
		if err != nil {
			return fmt.Errorf("更新 %s 的 storage_backend 失败: %w", row.ID, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("更新 %s 的 storage_backend 失败：行已不在 %s", row.ID, from)
		}
		copied++
	}
	logger.Info("附件字节搬迁完成", slog.Int("copied", copied))
	return nil
}

func copyOne(ctx context.Context, source, target storage.Store, row attachmentRow) error {
	reader, err := source.Open(ctx, row.Key)
	if err != nil {
		return fmt.Errorf("打开源对象 %s: %w", row.Key, err)
	}
	defer reader.Close()
	if err := target.Put(ctx, row.Key, reader, row.Size); err != nil {
		return fmt.Errorf("写入目标对象 %s: %w", row.Key, err)
	}
	return nil
}

func verify(
	ctx context.Context,
	pool *pgxpool.Pool,
	source, target storage.Store,
	from, to string,
	logger *slog.Logger,
) error {
	rows, err := queryRows(ctx, pool, to)
	if err != nil {
		return err
	}
	pending, err := queryRows(ctx, pool, from)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("仍有 %d 行 storage_backend=%s，尚未搬迁", len(pending), from)
	}
	for _, row := range rows {
		sourceHash, sourceSize, err := hashObject(ctx, source, row.Key)
		if err != nil {
			return fmt.Errorf("读取源对象 %s 失败: %w", row.Key, err)
		}
		targetHash, targetSize, err := hashObject(ctx, target, row.Key)
		if err != nil {
			return fmt.Errorf("读取目标对象 %s 失败: %w", row.Key, err)
		}
		if sourceHash != targetHash || sourceSize != targetSize || sourceSize != row.Size {
			return fmt.Errorf("对象 %s 校验失败（源 %s/%d，目标 %s/%d，账本 %d）",
				row.Key, sourceHash, sourceSize, targetHash, targetSize, row.Size)
		}
	}
	logger.Info("附件字节校验完成", slog.Int("rows", len(rows)))
	return nil
}

func hashObject(ctx context.Context, store storage.Store, key string) (string, int64, error) {
	reader, err := store.Open(ctx, key)
	if err != nil {
		return "", 0, err
	}
	defer reader.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func queryRows(ctx context.Context, pool *pgxpool.Pool, backend string) ([]attachmentRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, stored_key, size_bytes, storage_backend
		   FROM attachments WHERE storage_backend = $1 ORDER BY id`, backend)
	if err != nil {
		return nil, fmt.Errorf("查询 storage_backend=%s 的附件失败: %w", backend, err)
	}
	defer rows.Close()
	var result []attachmentRow
	for rows.Next() {
		var row attachmentRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Size, &row.Backend); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
