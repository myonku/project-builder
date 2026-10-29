package reminders

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/myonku/project-builder/internal/platform/mail"
)

// Dispatcher polls persisted due plans; business tasks are never scanned here.
type Dispatcher struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
}

// NewDispatcher 构造派发器。
func NewDispatcher(service *Service, interval time.Duration, logger *slog.Logger) *Dispatcher {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{service: service, interval: interval, logger: logger}
}

// Run 阻塞运行，直到 ctx 被取消。
//
// 启动时先跑一轮：否则一次重启会让所有到期提醒最多推迟一个周期，
// 而"重启后提醒变慢"是很难被联想到配置上的现象。
func (d *Dispatcher) Run(ctx context.Context) {
	d.logger.Info("提醒派发器已启动", slog.Duration("interval", d.interval))
	d.runOnce(ctx)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("提醒派发器已停止")
			return
		case <-ticker.C:
			d.runOnce(ctx)
		}
	}
}

// runOnce executes one bounded dispatch round.
//
// 错误只记录不中断：一轮失败（数据库抖动、SMTP 超时）不该让派发器退出，
// 否则服务会静默地永远不再提醒，而日志里只有一条早已滚走的错误。
func (d *Dispatcher) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	dispatched, err := d.service.Dispatch(ctx)
	if err != nil {
		d.logger.Error("派发提醒失败", slog.Any("cause", err))
		return
	}

	// 失败逐条按 WARN 记录：FAILED 的提醒需要人排查（需求文档 5.7
	// "邮件失败可查询原因"），而日志是排查的第一入口。
	// 邮件未启用属于配置状态而不是故障，因此降为 DEBUG，避免每周期刷屏
	// 掩盖真正的失败。
	for _, reason := range dispatched.Errors {
		level := slog.LevelWarn
		if strings.Contains(reason, mail.ErrDisabled.Error()) {
			level = slog.LevelDebug
		}
		d.logger.Log(ctx, level, "提醒发送失败", slog.String("reason", reason))
	}

	if dispatched.Sent > 0 || dispatched.Failed > 0 {
		d.logger.Info("提醒周期完成",
			slog.Int("sent", dispatched.Sent),
			slog.Int("failed", dispatched.Failed),
			slog.Int("skipped", dispatched.Skipped),
		)
	}
}
