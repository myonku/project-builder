package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP 是一个只说够用的 SMTP 服务器，用于在**不连外网**的前提下
// 验证整轮会话：EHLO → AUTH → MAIL → RCPT → DATA → QUIT。
//
// 为什么值得写它：SMTP 发送是"只在生产才第一次执行"的典型代码路径，
// 一旦线上才跑，任何拼装错误都会以"邮件没收到"的形式出现，排查成本极高。
type fakeSMTP struct {
	listener net.Listener
	// startTLS 控制是否在 EHLO 回复里声明 STARTTLS。
	startTLS bool

	mu       sync.Mutex
	authLine string
	from     string
	to       []string
	data     string
	messages int
}

func newFakeSMTP(t *testing.T, startTLS bool) *fakeSMTP {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动假 SMTP 服务器失败: %v", err)
	}
	server := &fakeSMTP{listener: listener, startTLS: startTLS}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

// address 返回 host:port。
func (f *fakeSMTP) address(t *testing.T) (string, int) {
	t.Helper()

	host, port, err := net.SplitHostPort(f.listener.Addr().String())
	if err != nil {
		t.Fatalf("解析地址失败: %v", err)
	}
	numeric, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}
	return host, numeric
}

// snapshot 返回本次会话捕获到的内容。
func (f *fakeSMTP) snapshot() (auth, from string, to []string, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authLine, f.from, append([]string(nil), f.to...), f.data
}

func (f *fakeSMTP) serve() {
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(connection)
	}
}

func (f *fakeSMTP) handle(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)

	reply := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}

	reply("220 fake.local ESMTP ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(command)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			// 多行能力声明必须以 "250-" 续行、"250 " 结束。
			reply("250-fake.local")
			reply("250-8BITMIME")
			reply("250-SMTPUTF8")
			if f.startTLS {
				reply("250-STARTTLS")
			}
			reply("250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(upper, "AUTH"):
			f.mu.Lock()
			f.authLine = command
			f.mu.Unlock()
			reply("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM"):
			f.mu.Lock()
			f.from = command
			f.mu.Unlock()
			reply("250 2.1.0 Ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			f.mu.Lock()
			f.to = append(f.to, command)
			f.mu.Unlock()
			reply("250 2.1.5 Ok")
		case strings.HasPrefix(upper, "DATA"):
			reply("354 End data with <CR><LF>.<CR><LF>")
			var payload strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				payload.WriteString(dataLine)
			}
			f.mu.Lock()
			f.data = payload.String()
			f.messages++
			f.mu.Unlock()
			reply("250 2.0.0 Ok: queued")
		case strings.HasPrefix(upper, "QUIT"):
			reply("221 2.0.0 Bye")
			return
		case strings.HasPrefix(upper, "RSET"), strings.HasPrefix(upper, "NOOP"):
			reply("250 2.0.0 Ok")
		default:
			reply("250 2.0.0 Ok")
		}
	}
}

// TestSMTPSendSession 覆盖一整轮真实 SMTP 会话。
func TestSMTPSendSession(t *testing.T) {
	t.Parallel()

	server := newFakeSMTP(t, false)
	host, port := server.address(t)

	sender, err := NewSMTP(SMTPConfig{
		Host: host, Port: port,
		Username: "user@example.com", Password: "secret",
		FromAddress: "user@example.com", FromName: "项目计划管理系统",
		UseTLS: false, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}

	err = sender.Send(context.Background(), Message{
		To:      "recipient@example.com",
		ToName:  "张三",
		Subject: "任务「迁移方案」将在 1 个工作日内到期",
		Body:    "项目：迁移\n任务：迁移方案\n状态：进行中\n链接：http://localhost/tasks/1",
	})
	if err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}

	auth, from, to, data := server.snapshot()
	if !strings.HasPrefix(auth, "AUTH") {
		t.Fatalf("未发生身份验证，实际收到 %q", auth)
	}
	if !strings.Contains(from, "user@example.com") {
		t.Fatalf("MAIL FROM 不含发件地址: %q", from)
	}
	if len(to) != 1 || !strings.Contains(to[0], "recipient@example.com") {
		t.Fatalf("RCPT TO 不含收件地址: %v", to)
	}

	// 头与正文的断言。
	if !strings.Contains(data, "MIME-Version: 1.0") {
		t.Error("缺少 MIME-Version 头")
	}
	if !strings.Contains(data, "Content-Type: text/plain; charset=UTF-8") {
		t.Error("缺少 UTF-8 正文声明")
	}
	// 中文主题必须按 RFC 2047 编码，否则客户端显示乱码。
	if !strings.Contains(data, "Subject: =?utf-8?") {
		t.Errorf("主题未按 RFC 2047 编码:\n%s", data)
	}
	if !strings.Contains(data, "项目：迁移") {
		t.Error("正文未按 UTF-8 原样发送")
	}
	// 行尾必须是 CRLF。
	if strings.Contains(strings.ReplaceAll(data, "\r\n", ""), "\n") {
		t.Error("邮件内容里存在裸 LF 行尾")
	}
	if !strings.Contains(data, "\r\n\r\n") {
		t.Error("缺少头与正文之间的空行")
	}
}

// TestSMTPRefusesSilentDowngrade 锁住"要求 TLS 但服务端不支持时必须失败"。
//
// 静默降级会把账号密码发到明文连接上，这是最危险的一类"为了可用性牺牲安全"。
func TestSMTPRefusesSilentDowngrade(t *testing.T) {
	t.Parallel()

	// 服务器不声明 STARTTLS。
	server := newFakeSMTP(t, false)
	host, port := server.address(t)

	sender, err := NewSMTP(SMTPConfig{
		Host: host, Port: port,
		Username: "user@example.com", Password: "secret",
		FromAddress: "user@example.com",
		UseTLS:      true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}

	err = sender.Send(context.Background(), Message{
		To: "recipient@example.com", Subject: "x", Body: "y",
	})
	if err == nil {
		t.Fatal("服务端不支持 STARTTLS 时必须失败，实际成功")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("错误信息应指出 STARTTLS 问题，实际 %v", err)
	}

	auth, _, _, data := server.snapshot()
	if auth != "" || data != "" {
		t.Fatal("拒绝发送时不得把凭据或正文交给服务端")
	}
}

// TestSMTPHeaderInjectionIsPrevented 覆盖邮件头注入。
func TestSMTPHeaderInjectionIsPrevented(t *testing.T) {
	t.Parallel()

	server := newFakeSMTP(t, false)
	host, port := server.address(t)

	sender, err := NewSMTP(SMTPConfig{
		Host: host, Port: port,
		FromAddress: "user@example.com", UseTLS: false, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}

	// 主题里塞进换行 + 额外头：必须被折叠成一行，而不是变成真的 Bcc。
	err = sender.Send(context.Background(), Message{
		To:      "recipient@example.com",
		Subject: "正常主题\r\nBcc: attacker@example.com",
		Body:    "正文",
	})
	if err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}

	_, _, _, data := server.snapshot()
	headerBlock, _, found := strings.Cut(data, "\r\n\r\n")
	if !found {
		t.Fatal("邮件缺少头与正文之间的空行")
	}
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("邮件头被注入:\n%s", data)
		}
	}
	// 注入的换行被主题编码吞掉（成为主题的一部分），因此"Bcc"字样仍可能在
	// 编码后的主题里出现——这是安全的：它出现在主题值内部，而不是一个头。
	if !strings.Contains(headerBlock, "Subject: ") {
		t.Fatal("缺少 Subject 头")
	}
	// 真正的防线是"头部值里不得出现裸换行"，这里直接单测那一段。
	var builder strings.Builder
	writeHeader(&builder, "X-Test", "正常\r\nBcc: attacker@example.com")
	if strings.Contains(builder.String(), "\r\nBcc:") {
		t.Fatalf("writeHeader 未折叠换行: %q", builder.String())
	}
	if strings.Count(builder.String(), "\r\n") != 1 {
		t.Fatalf("writeHeader 应只产生一行: %q", builder.String())
	}
}

// TestSMTPXOAUTH2Payload 覆盖 XOAUTH2 的载荷格式。
//
// 官方文档给出的认证方法是 OAuth2/Modern Auth，对应的 SMTP 机制就是 XOAUTH2。
// 载荷格式写错时服务器只会回一个含糊的 535，因此这里把格式钉住。
func TestSMTPXOAUTH2Payload(t *testing.T) {
	t.Parallel()

	server := newFakeSMTP(t, false)
	host, port := server.address(t)

	sender, err := NewSMTP(SMTPConfig{
		Host: host, Port: port, Username: "user@example.com",
		FromAddress: "user@example.com", UseTLS: false,
		AuthMethod: "xoauth2", OAuth2AccessToken: "ya29.example-token",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}
	if err := sender.Send(context.Background(), Message{
		To: "recipient@example.com", Subject: "s", Body: "b",
	}); err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}

	auth, _, _, _ := server.snapshot()
	if !strings.HasPrefix(auth, "AUTH XOAUTH2 ") {
		t.Fatalf("未使用 XOAUTH2，实际收到 %q", auth)
	}
	// base64 解出的载荷必须是 user=<用户>\\x01auth=Bearer <令牌>\\x01\\x01
	encoded := strings.TrimPrefix(auth, "AUTH XOAUTH2 ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("载荷不是合法 base64: %v", err)
	}
	want := "user=user@example.com\x01auth=Bearer ya29.example-token\x01\x01"
	if string(decoded) != want {
		t.Fatalf("XOAUTH2 载荷 = %q，期望 %q", decoded, want)
	}
}

// TestSMTPXOAUTH2RequiresToken 覆盖缺令牌时报错而不是发出空凭据。
func TestSMTPXOAUTH2RequiresToken(t *testing.T) {
	t.Parallel()

	server := newFakeSMTP(t, false)
	host, port := server.address(t)

	sender, err := NewSMTP(SMTPConfig{
		Host: host, Port: port, Username: "user@example.com",
		FromAddress: "user@example.com", UseTLS: false,
		AuthMethod: "xoauth2", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}
	if err := sender.Send(context.Background(), Message{
		To: "recipient@example.com", Subject: "s", Body: "b",
	}); err == nil {
		t.Fatal("缺少访问令牌时应报错")
	}
}

// TestSMTPRequiresRecipient 覆盖参数校验。
func TestSMTPRequiresRecipient(t *testing.T) {
	t.Parallel()

	sender, err := NewSMTP(SMTPConfig{
		Host: "127.0.0.1", Port: 2525, FromAddress: "a@example.com",
	})
	if err != nil {
		t.Fatalf("构造 SMTP 发送器失败: %v", err)
	}
	if err := sender.Send(context.Background(), Message{Subject: "x"}); err == nil {
		t.Fatal("缺少收件地址时应报错")
	}
}

func TestNewSMTPValidatesConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  SMTPConfig
	}{
		{name: "缺少主机", cfg: SMTPConfig{Port: 587, FromAddress: "a@example.com"}},
		{name: "端口非法", cfg: SMTPConfig{Host: "h", Port: 0, FromAddress: "a@example.com"}},
		{name: "端口超范围", cfg: SMTPConfig{Host: "h", Port: 70000, FromAddress: "a@example.com"}},
		{name: "缺少发件地址", cfg: SMTPConfig{Host: "h", Port: 587}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewSMTP(tc.cfg); err == nil {
				t.Fatal("非法配置应被拒绝")
			}
		})
	}
}

// TestDisabledSenderIsNotSuccess 锁住"未启用 ≠ 发送成功"。
func TestDisabledSenderIsNotSuccess(t *testing.T) {
	t.Parallel()

	err := Disabled{}.Send(context.Background(), Message{To: "a@example.com"})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Disabled.Send 应返回 ErrDisabled，实际 %v", err)
	}
}
