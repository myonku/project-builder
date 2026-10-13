package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/platform/mail"
)

// 本文件是**邮件通道的真实依赖测试**：把一封信投给 Mailpit，再通过它的 HTTP API
// 确认收到了。
//
// 为什么单独一条：
//   - `internal/platform/mail` 的单测用**假 SMTP 服务器**验证协议拼装，
//     它证明"我们会说 SMTP"，但不证明"真实服务器接受我们的会话顺序"；
//   - 应用侧的提醒用例注入的是 `FakeSender`，验证的是业务规则而不是传输；
//   - 真实服务商的投递由 `livecheck` 手动联调（需要真实凭据，不进 CI）。
//
// 于是这里补上中间那一段：**真实的 SMTP 传输**，依赖用 docker/compose.test.yml 里的
// Mailpit（SMTP 127.0.0.1:11025，HTTP 127.0.0.1:18025）。Mailpit 不校验凭据，
// 因此本用例不需要任何密码；依赖不可达时按本项目惯例 skip 而不是失败。

const (
	mailpitSMTPAddr = "127.0.0.1:11025"
	mailpitAPIURL   = "http://127.0.0.1:18025/api/v1/messages"
)

// TestMailpitAcceptsRealSMTPDelivery 覆盖"应用发送实现 → 真实 SMTP → 收件箱"这一段。
func TestMailpitAcceptsRealSMTPDelivery(t *testing.T) {
	if !mailpitReachable() {
		t.Skipf("跳过集成测试：Mailpit 不可达（%s）", mailpitAPIURL)
	}

	host, port := splitHostPort(t, mailpitSMTPAddr)
	sender, err := mail.NewSMTP(mail.SMTPConfig{
		Host:        host,
		Port:        port,
		FromAddress: "project-builder@test.local",
		FromName:    "项目计划管理系统（测试）",
		// Mailpit 监听明文端口：既不 STARTTLS 也不隐式 TLS。
		UseTLS:      false,
		ImplicitTLS: false,
		Timeout:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}

	// 主题里带唯一标记：Mailpit 是共享收件箱，必须能认出"这一封是我发的"。
	marker := fmt.Sprintf("mailpit-integration-%d", time.Now().UnixNano())
	subject := "【联调】" + marker + " 中文主题"

	if err := sender.Send(context.Background(), mail.Message{
		To:      "recipient@test.local",
		Subject: subject,
		Body:    "这是一封由集成测试投递的邮件。\n中文正文第二行。",
	}); err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	if !mailpitHasSubject(t, subject) {
		t.Fatalf("Mailpit 收件箱里没有找到主题为 %q 的邮件", subject)
	}
}

// mailpitReachable 报告 Mailpit 的 HTTP API 是否可用。
func mailpitReachable() bool {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(mailpitAPIURL)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// mailpitHasSubject 在 Mailpit 的收件箱里查找指定主题。
//
// 返回体形如 {"total":1,"messages":[{"Subject":"..."}]}；只取最近 50 封，
// 避免一个长期运行的 Mailpit 把响应撑大。
func mailpitHasSubject(t *testing.T, subject string) bool {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(mailpitAPIURL + "?limit=50")
	if err != nil {
		t.Fatalf("查询 Mailpit 失败: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Messages []struct {
			Subject string `json:"Subject"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("解析 Mailpit 响应失败: %v", err)
	}
	for _, message := range payload.Messages {
		if strings.Contains(message.Subject, subject) {
			return true
		}
	}
	return false
}

// splitHostPort 把 "host:port" 拆成两段。
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()

	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		t.Fatalf("地址 %q 不是 host:port 形式", addr)
	}
	port := 0
	if _, err := fmt.Sscanf(parts[1], "%d", &port); err != nil {
		t.Fatalf("端口 %q 无法解析: %v", parts[1], err)
	}
	return parts[0], port
}
