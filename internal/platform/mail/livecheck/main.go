//go:build live

// 这是一个**只在显式指定 tag 时编译**的联调程序：向真实 SMTP 服务器投递一封邮件。
//
// 用法（凭据只从环境变量读取，绝不写进仓库）：
//
//	go run -tags live ./internal/platform/mail/livecheck \
//	  -to myonku@outlook.com
//
// 存在的理由：SMTP 是"只在生产才第一次执行"的典型代码路径。
// 单元测试用假服务器验证了协议拼装，但**账号是否真能通过认证**、
// 服务器是否接受我们的会话顺序，只有连一次真实服务器才能知道。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/myonku/project-builder/internal/platform/mail"
)

func main() {
	to := flag.String("to", "", "收件地址（必填）")
	flag.Parse()

	if *to == "" {
		log.Fatal("必须用 -to 指定收件地址")
	}

	port, err := strconv.Atoi(envOr("MAIL_PORT", "587"))
	if err != nil {
		log.Fatalf("MAIL_PORT 非法: %v", err)
	}

	cfg := mail.SMTPConfig{
		Host:        envOr("MAIL_HOST", "smtp-mail.outlook.com"),
		Port:        port,
		Username:    os.Getenv("MAIL_USERNAME"),
		Password:    os.Getenv("MAIL_PASSWORD"),
		FromAddress: envOr("MAIL_FROM_ADDRESS", os.Getenv("MAIL_USERNAME")),
		FromName:    envOr("MAIL_FROM_NAME", "项目计划管理系统"),
		// 587 + STARTTLS 是官方页面给出的组合。
		UseTLS:            envOr("MAIL_USE_TLS", "true") == "true",
		ImplicitTLS:       os.Getenv("MAIL_IMPLICIT_TLS") == "true",
		AuthMethod:        os.Getenv("MAIL_AUTH_METHOD"),
		OAuth2AccessToken: os.Getenv("MAIL_OAUTH2_ACCESS_TOKEN"),
		Timeout:           30 * time.Second,
	}
	if cfg.AuthMethod == "xoauth2" && cfg.OAuth2AccessToken != "" && cfg.Password == "" {
		// xoauth2 不需要密码；用占位符让下面的凭据校验通过。
		cfg.Password = "-"
	}
	if cfg.Username == "" || cfg.Password == "" {
		log.Fatal("必须通过 MAIL_USERNAME / MAIL_PASSWORD 提供凭据")
	}

	sender, err := mail.NewSMTP(cfg)
	if err != nil {
		log.Fatalf("构造发送器失败: %v", err)
	}

	body := fmt.Sprintf(`这是一封来自 project-builder 的联调测试邮件。

它用于验证以下事实：
  1. SMTP 服务器 %s:%d 可达；
  2. 使用配置的账号可以通过身份验证；
  3. STARTTLS 协商成功；
  4. 中文主题（RFC 2047）与中文正文（UTF-8）在真实客户端可正常显示。

发送时刻（UTC）：%s

如果你在收件箱里看到这封邮件且中文不乱码，说明发送通道可用。`,
		cfg.Host, cfg.Port, time.Now().UTC().Format(time.RFC3339))

	message := mail.Message{
		To:      *to,
		Subject: "【联调】project-builder 邮件提醒通道自检 —— 中文主题测试",
		Body:    body,
	}

	start := time.Now()
	if err := sender.Send(context.Background(), message); err != nil {
		log.Fatalf("发送失败（耗时 %s）: %v", time.Since(start), err)
	}
	fmt.Printf("发送成功：%s → %s（耗时 %s）\n", cfg.FromAddress, *to, time.Since(start))
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
