package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/myonku/project-builder/internal/domain"
)

// sessionKeyPrefix 是会话键前缀。
//
// 会话数据存 Redis 而非 Cookie（接口文档 2.2：会话数据存放 Redis，
// Cookie 不直接承载权限数据）。Cookie 中只有不可预测的随机令牌。
const sessionKeyPrefix = "pb:session:"

// Session 是会话状态。
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ErrSessionBackendDisabled 表示 Redis 未启用，会话功能不可用。
var ErrSessionBackendDisabled = domain.New(domain.CodeInternalError,
	"会话存储不可用（Redis 已禁用）")

// SessionStore 负责会话的持久化。
//
// 定义在消费方（本模块）而不是基础设施层：只有本模块需要该接口，
// 且接口只在测试需要替身时才存在（见 docs/01-Go服务端架构设计.md 4.4）。
type SessionStore interface {
	// Save 写入会话，过期时间由会话自身的 ExpiresAt 决定。
	Save(ctx context.Context, session Session) error
	// Get 读取会话；不存在或已过期返回 domain.ErrAuthenticationRequired。
	Get(ctx context.Context, token string) (Session, error)
	// Delete 撤销会话。删除不存在的会话不算错误（登出必须幂等）。
	Delete(ctx context.Context, token string) error
	// Available 报告会话后端是否可用。
	Available() bool
}

// RedisSessionStore 是 SessionStore 的 Redis 实现。
type RedisSessionStore struct {
	client *redis.Client
	// enabled 来自配置；为 false 时所有写操作返回明确错误而不是静默失败。
	enabled bool
}

// NewRedisSessionStore 构造 Redis 会话存储。
//
// client 可以为 nil（Redis 禁用场景），此时 Available() 返回 false。
func NewRedisSessionStore(client *redis.Client, enabled bool) *RedisSessionStore {
	return &RedisSessionStore{client: client, enabled: enabled && client != nil}
}

// Available 报告会话后端是否可用。
func (s *RedisSessionStore) Available() bool { return s != nil && s.enabled }

func (s *RedisSessionStore) key(token string) string { return sessionKeyPrefix + token }

// Save 写入会话。
func (s *RedisSessionStore) Save(ctx context.Context, session Session) error {
	if !s.Available() {
		return ErrSessionBackendDisabled
	}

	ttl := time.Until(session.ExpiresAt)
	if ttl <= 0 {
		return domain.ErrInternal.WithCause(errors.New("会话过期时间已过，拒绝写入"))
	}

	payload, err := json.Marshal(session)
	if err != nil {
		return domain.ErrInternal.WithCause(fmt.Errorf("序列化会话失败: %w", err))
	}

	if err := s.client.Set(ctx, s.key(session.Token), payload, ttl).Err(); err != nil {
		return domain.ErrInternal.WithCause(fmt.Errorf("写入会话失败: %w", err))
	}
	return nil
}

// Get 读取会话。不存在时统一返回"需要登录"，不区分"过期"与"伪造"，
// 避免给攻击者提供区分依据。
func (s *RedisSessionStore) Get(ctx context.Context, token string) (Session, error) {
	var session Session
	if !s.Available() {
		return session, ErrSessionBackendDisabled
	}
	if token == "" {
		return session, domain.ErrAuthenticationRequired
	}

	payload, err := s.client.Get(ctx, s.key(token)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return session, domain.ErrAuthenticationRequired
		}
		return session, domain.ErrInternal.WithCause(fmt.Errorf("读取会话失败: %w", err))
	}

	if err := json.Unmarshal(payload, &session); err != nil {
		return Session{}, domain.ErrInternal.WithCause(fmt.Errorf("解析会话失败: %w", err))
	}
	// 双重过期判断：Redis TTL 是主保证，这里防御时钟漂移与手工写入。
	if !session.ExpiresAt.IsZero() && time.Now().UTC().After(session.ExpiresAt) {
		return Session{}, domain.ErrAuthenticationRequired
	}
	return session, nil
}

// Delete 撤销会话。Redis 禁用时视为成功，保证登出流程不会因基础设施状态而卡住。
func (s *RedisSessionStore) Delete(ctx context.Context, token string) error {
	if !s.Available() || token == "" {
		return nil
	}
	if err := s.client.Del(ctx, s.key(token)).Err(); err != nil {
		return domain.ErrInternal.WithCause(fmt.Errorf("撤销会话失败: %w", err))
	}
	return nil
}

// 编译期确认接口实现。
var _ SessionStore = (*RedisSessionStore)(nil)
