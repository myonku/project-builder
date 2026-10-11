package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig 描述一个 SMTP 端点。
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// FromAddress 是信封发件人与 From 头。
	FromAddress string
	FromName    string
	// UseTLS 为真时在**明文连接之上**协商 STARTTLS（端口 587 的常规做法）。
	UseTLS bool
	// ImplicitTLS 为真时直接以 TLS 连接（端口 465 的做法）。
	//
	// 与 UseTLS 分开是因为二者不可混用：465 上先明文再 STARTTLS 会直接失败，
	// 而 587 上直接 TLS 也连不上。把它们合成一个布尔量必然产生"配置看起来对、
	// 但永远连不上"的状态。
	ImplicitTLS bool
	// Timeout 是单次发送的总预算（默认 30 秒）。
	Timeout time.Duration
	// AuthMethod 指定认证机制："login"（默认）、"plain" 或 "xoauth2"。
	//
	// 留空表示"login，失败且服务器说不认识该机制时改用 plain"。
	// 之所以需要显式指定：官方文档给出的"OAuth2/Modern Auth"要求 xoauth2，
	// 而它需要一枚外部获取的访问令牌；把机制写成配置项，
	// 换认证方式就不必改代码。
	AuthMethod string
	// OAuth2AccessToken 仅在 AuthMethod 为 xoauth2 时使用。
	//
	// 只接受**访问令牌**，不接受 refresh token：换取访问令牌所需的
	// 客户端注册信息（client_id / tenant）由部署方持有，
	// 本服务不去实现一轮 OAuth2 授权码流程。
	OAuth2AccessToken string
}

// SMTP 是基于 net/smtp 的 Sender 实现。
type SMTP struct {
	cfg SMTPConfig
}

var _ Sender = (*SMTP)(nil)

// NewSMTP 构造 SMTP 发送器。
//
// 刻意不在这里做"能否连通"的探测：启动时的一次探测无法代表运行时的可达性，
// 反而会让服务因为邮件服务器暂时不可用而拒绝启动。真正的错误在发送时记录到提醒里。
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("mail: SMTP 主机不能为空")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("mail: SMTP 端口非法: %d", cfg.Port)
	}
	if strings.TrimSpace(cfg.FromAddress) == "" {
		return nil, errors.New("mail: 发件地址不能为空")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &SMTP{cfg: cfg}, nil
}

// Send 实现 Sender。
//
// 认证机制会按"LOGIN 优先、PLAIN 兜底"尝试：真实环境的实测结果是
// smtp-mail.outlook.com **不接受 AUTH PLAIN**（返回
// `504 5.7.4 Unrecognized authentication type`），只接受 AUTH LOGIN。
// 两种机制都实现，避免把可移植性押在某一家服务器上。
//
// 兜底是**有条件**的：只有当服务器明确表示"不认识这个机制"时才换一种。
// 密码错误（535）立刻失败，不做第二次尝试——重复提交错误凭据可能触发
// 账号锁定策略，而那时我们会把"密码错了"报成更难懂的错误。
func (s *SMTP) Send(ctx context.Context, message Message) error {
	if strings.TrimSpace(message.To) == "" {
		return errors.New("mail: 收件地址不能为空")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if s.cfg.AuthMethod == "xoauth2" {
		return s.sendWith(ctx, message, s.xoauth2Auth)
	}
	if s.cfg.AuthMethod == "plain" {
		return s.sendWith(ctx, message, s.plainAuth)
	}

	firstErr := s.sendWith(ctx, message, s.loginAuth)
	if firstErr == nil {
		return nil
	}
	if !isUnrecognizedAuthMechanism(firstErr) {
		return firstErr
	}

	// 服务器不认识 LOGIN：换 PLAIN 重试一次（需要新连接，会话已被拒绝命令打断）。
	secondErr := s.sendWith(ctx, message, s.plainAuth)
	if secondErr == nil {
		return nil
	}
	return fmt.Errorf("mail: 两种认证机制都被拒绝（LOGIN: %v；PLAIN: %v）", firstErr, secondErr)
}

// isUnrecognizedAuthMechanism 报告错误是否表示"服务器不认识该认证机制"。
func isUnrecognizedAuthMechanism(err error) bool {
	message := err.Error()
	return strings.Contains(message, "504") ||
		strings.Contains(message, "Unrecognized authentication type")
}

// loginAuth 返回 AUTH LOGIN 认证器（用户名与密码分两次以 base64 提交）。
//
// net/smtp 只自带 PlainAuth 与 CRAMMD5Auth，而 LOGIN 是 Outlook 唯一接受的机制，
// 因此这里按 net/smtp.Auth 的协议实现它。
func (s *SMTP) loginAuth() smtp.Auth {
	return &loginAuth{username: s.cfg.Username, password: s.cfg.Password}
}

// plainAuth 返回 AUTH PLAIN 认证器。
func (s *SMTP) plainAuth() smtp.Auth {
	return smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
}

// xoauth2Auth 返回 AUTH XOAUTH2 认证器。
func (s *SMTP) xoauth2Auth() smtp.Auth {
	return &xoauth2Auth{username: s.cfg.Username, token: s.cfg.OAuth2AccessToken}
}

// xoauth2Auth 实现 Gmail/Outlook 通用的 AUTH XOAUTH2 流程。
//
// 该机制的载荷是一个"以 \\x01 分隔的键值串"，整体交 base64 编码后作为
// AUTH 命令的初始响应（net/smtp 会负责 base64 编码，因此这里返回原始字节）。
type xoauth2Auth struct {
	username string
	token    string
}

// Start 发起 AUTH XOAUTH2。
func (a *xoauth2Auth) Start(*smtp.ServerInfo) (string, []byte, error) {
	if strings.TrimSpace(a.token) == "" {
		return "", nil, errors.New("mail: xoauth2 认证需要访问令牌")
	}
	payload := "user=" + a.username + "\x01auth=Bearer " + a.token + "\x01\x01"
	return "XOAUTH2", []byte(payload), nil
}

// Next 处理服务器的错误挑战。
//
// 令牌被拒时服务器会发一段 base64 的错误说明并等待客户端继续；
// 此时必须回一个**空行**，服务器才会给出真正的失败状态码。
// 不回空行会让连接挂在那里直到超时——表现为"发送卡住"而不是"认证失败"。
func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil
	}
	return nil, nil
}

// loginAuth 实现 smtp.Auth 的 AUTH LOGIN 流程。
type loginAuth struct {
	username string
	password string
}

// Start 发起 AUTH LOGIN。
func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

// Next 依次回答服务器的 Username / Password 挑战。
//
// 按提示内容判断而不是按调用次序：部分服务器会重复挑战，
// 那就应该重复给出上一次的答案，而不是错位提交密码。
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(prompt, "username"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "password"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("mail: AUTH LOGIN 收到无法识别的挑战: %s", fromServer)
	}
}

// sendWith 用指定的认证机制完成一次完整会话。
func (s *SMTP) sendWith(ctx context.Context, message Message, auth func() smtp.Auth) error {
	// 连接阶段用 ctx 的截止时间，而不是固定超时：调用方（派发器）可能给出更紧的预算。
	deadline := time.Now().Add(s.cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Deadline: deadline}

	var connection net.Conn
	var err error
	if s.cfg.ImplicitTLS {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("mail: 连接 SMTP 服务器失败: %w", err)
	}
	defer func() { _ = connection.Close() }()

	return s.sendOverConnection(ctx, connection, deadline, message, auth)
}

// sendOverConnection 在已建立的连接上完成整轮 SMTP 会话。
func (s *SMTP) sendOverConnection(
	ctx context.Context, connection net.Conn, deadline time.Time, message Message,
	authFunc func() smtp.Auth,
) error {
	// 整个会话的每一步都受同一个截止时间约束，避免某一步卡住让请求悬挂。
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("mail: 设置连接超时失败: %w", err)
	}

	client, err := smtp.NewClient(connection, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: 建立 SMTP 会话失败: %w", err)
	}

	// STARTTLS：仅当配置要求且服务端声明支持时执行。
	// 配置要求但服务端不支持时必须**失败**而不是降级为明文——
	// 静默降级会把凭据发到明文连接上。
	if s.cfg.UseTLS && !s.cfg.ImplicitTLS {
		supported, _ := client.Extension("STARTTLS")
		if !supported {
			_ = client.Close()
			return errors.New("mail: 服务器未声明支持 STARTTLS，拒绝以明文发送凭据")
		}
		if err := client.StartTLS(&tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			_ = client.Close()
			return fmt.Errorf("mail: STARTTLS 协商失败: %w", err)
		}
	}

	if s.cfg.Username != "" && authFunc != nil {
		// 个人版 Outlook 在启用"应用密码"时走的就是这条路径
		// （官方页面写的 OAuth2/Modern Auth 需要应用注册，本系统不用它）。
		if err := client.Auth(authFunc()); err != nil {
			_ = client.Close()
			return fmt.Errorf("mail: SMTP 身份验证失败: %w", err)
		}
	}

	if err := client.Mail(s.cfg.FromAddress); err != nil {
		_ = client.Close()
		return fmt.Errorf("mail: 设置发件人失败: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		_ = client.Close()
		return fmt.Errorf("mail: 设置收件人失败: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("mail: 开始写入邮件正文失败: %w", err)
	}
	if _, err := writer.Write(s.buildRFC822(message)); err != nil {
		_ = writer.Close()
		_ = client.Close()
		return fmt.Errorf("mail: 写入邮件正文失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		_ = client.Close()
		return fmt.Errorf("mail: 提交邮件正文失败: %w", err)
	}

	// Quit 的返回值在部分服务器上是"连接被对端关闭"，此时邮件其实已经收下。
	// 因此这里以错误为准只在它明确反映命令失败时返回——net/smtp 的 Quit
	// 失败无法与"已接收"区分，故记录为错误但不阻断（由调用方决定是否重试，
	// 而 dedupe_key 保证重试不会重复发送）。
	if err := client.Quit(); err != nil {
		return fmt.Errorf("mail: 结束 SMTP 会话失败（邮件可能已投递）: %w", err)
	}

	// ctx 在会话过程中被取消时也要让调用方知道。
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// buildRFC822 组装邮件内容。
//
// 关键点：
//   - Subject 必须按 RFC 2047 编码，否则中文主题在客户端会变成乱码；
//   - 正文用 UTF-8 明文并声明 Content-Type，避免被当成 Latin-1；
//   - 行尾统一 CRLF，这是 SMTP 的要求；
//   - From/To 的显示名同样需要编码。
func (s *SMTP) buildRFC822(message Message) []byte {
	var builder strings.Builder

	from := s.cfg.FromAddress
	if name := strings.TrimSpace(s.cfg.FromName); name != "" {
		from = formatAddress(name, s.cfg.FromAddress)
	}
	to := message.To
	if name := strings.TrimSpace(message.ToName); name != "" {
		to = formatAddress(name, message.To)
	}

	writeHeader(&builder, "From", from)
	writeHeader(&builder, "To", to)
	writeHeader(&builder, "Subject", mime.QEncoding.Encode("utf-8", message.Subject))
	writeHeader(&builder, "Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader(&builder, "MIME-Version", "1.0")
	writeHeader(&builder, "Content-Type", "text/plain; charset=UTF-8")
	writeHeader(&builder, "Content-Transfer-Encoding", "8bit")
	builder.WriteString("\r\n")

	// 正文一律用 CRLF 行尾。
	normalized := strings.ReplaceAll(message.Body, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\n", "\r\n")
	builder.WriteString(normalized)
	if !strings.HasSuffix(normalized, "\r\n") {
		builder.WriteString("\r\n")
	}
	return []byte(builder.String())
}

// writeHeader 写一行头。
//
// 头部不得出现裸换行：那会让攻击者（或只是粗心的调用方）注入额外的头，
// 例如偷偷追加一个 Bcc。这里统一折叠为空格。
func writeHeader(builder *strings.Builder, name, value string) {
	sanitized := strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	builder.WriteString(name)
	builder.WriteString(": ")
	builder.WriteString(sanitized)
	builder.WriteString("\r\n")
}

// formatAddress 生成 "显示名 <地址>"，显示名按 RFC 2047 编码。
func formatAddress(name, address string) string {
	return mime.QEncoding.Encode("utf-8", name) + " <" + address + ">"
}
