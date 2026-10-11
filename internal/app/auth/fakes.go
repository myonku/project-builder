package auth

import (
	"context"
	"sync"
	"time"

	"github.com/myonku/project-builder/internal/domain"
)

// 本文件提供测试替身。放在普通源文件（而非 _test.go）是为了让集成测试与
// 其他包的测试都能复用。替身刻意实现与真实仓储相同的错误语义，
// 避免"测试通过但真实实现出错"——这正是 Python 版本的结构性盲区。

// FakeUserRepository 是内存版 UserRepository。
type FakeUserRepository struct {
	mu sync.Mutex
	// byID 保存全部用户；键为 {id,lower(username),lower(email)} → id 的映射由 byIdentifier 处理。
	byID    map[string]UserRecord
	order   []string
	nowFunc func() time.Time

	// 注入失败，用于覆盖故障路径。
	CreateErr error
	FindErr   error
}

// NewFakeUserRepository 构造内存仓储。
func NewFakeUserRepository(now func() time.Time) *FakeUserRepository {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &FakeUserRepository{
		byID:    map[string]UserRecord{},
		nowFunc: now,
	}
}

// CreateUser 实现 UserRepository，并模拟唯一约束冲突。
func (r *FakeUserRepository) CreateUser(_ context.Context, in NewUser) (UserRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.CreateErr != nil {
		return UserRecord{}, r.CreateErr
	}
	for _, existing := range r.byID {
		if existing.Username == in.Username {
			return UserRecord{}, domain.ErrConflict.WithField("username", "该用户名已被占用")
		}
		if existing.Email == in.Email {
			return UserRecord{}, domain.ErrConflict.WithField("email", "该邮箱已被注册")
		}
	}

	now := r.nowFunc().UTC()
	record := UserRecord{
		ID:              in.ID,
		Username:        in.Username,
		Email:           in.Email,
		DisplayName:     in.DisplayName,
		PasswordHash:    in.PasswordHash,
		Status:          in.Status,
		SystemRole:      in.SystemRole,
		Timezone:        in.Timezone,
		ReminderEnabled: in.ReminderEnabled,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	r.byID[record.ID] = record
	r.order = append(r.order, record.ID)
	return record, nil
}

// FindByID 实现 UserRepository。
func (r *FakeUserRepository) FindByID(_ context.Context, id string) (UserRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.FindErr != nil {
		return UserRecord{}, r.FindErr
	}
	record, ok := r.byID[id]
	if !ok {
		return UserRecord{}, domain.New(domain.CodeUserNotFound, "User not found")
	}
	return record, nil
}

// FindByIdentifier 实现 UserRepository，按用户名或邮箱匹配（不区分大小写）。
func (r *FakeUserRepository) FindByIdentifier(_ context.Context, identifier string) (UserRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.FindErr != nil {
		return UserRecord{}, r.FindErr
	}
	for _, id := range r.order {
		record := r.byID[id]
		if equalFold(record.Username, identifier) || equalFold(record.Email, identifier) {
			return record, nil
		}
	}
	// 与真实实现一致的语义：登录路径把"无行"翻译为凭据错误。
	return UserRecord{}, domain.ErrInvalidCredentials
}

// TouchLastLogin 实现 UserRepository。
func (r *FakeUserRepository) TouchLastLogin(_ context.Context, id string, at time.Time) (UserRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.byID[id]
	if !ok {
		return UserRecord{}, domain.New(domain.CodeUserNotFound, "User not found")
	}
	stamp := at.UTC()
	record.LastLoginAt = &stamp
	record.UpdatedAt = r.nowFunc().UTC()
	r.byID[id] = record
	return record, nil
}

// UpdatePasswordHash 实现 UserRepository。
func (r *FakeUserRepository) UpdatePasswordHash(_ context.Context, id, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.byID[id]
	if !ok {
		return domain.New(domain.CodeUserNotFound, "User not found")
	}
	record.PasswordHash = hash
	r.byID[id] = record
	return nil
}

// Stored 返回指定用户的当前记录，供断言使用。
func (r *FakeUserRepository) Stored(id string) (UserRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.byID[id]
	return record, ok
}

// Count 返回用户数量。
func (r *FakeUserRepository) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// FakeSessionStore 是内存版 SessionStore。
type FakeSessionStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	// SaveErr 注入写入失败。
	SaveErr error
	// available 为 false 时模拟 Redis 被禁用。
	available bool
}

// NewFakeSessionStore 构造可用的内存会话存储。
func NewFakeSessionStore() *FakeSessionStore {
	return &FakeSessionStore{sessions: map[string]Session{}, available: true}
}

// NewUnavailableSessionStore 构造不可用的会话存储，用于覆盖 Redis 禁用场景。
func NewUnavailableSessionStore() *FakeSessionStore {
	return &FakeSessionStore{sessions: map[string]Session{}, available: false}
}

// Available 实现 SessionStore。
func (s *FakeSessionStore) Available() bool { return s != nil && s.available }

// Save 实现 SessionStore。
func (s *FakeSessionStore) Save(_ context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.SaveErr != nil {
		return s.SaveErr
	}
	s.sessions[session.Token] = session
	return nil
}

// Get 实现 SessionStore。
func (s *FakeSessionStore) Get(_ context.Context, token string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.available {
		return Session{}, ErrSessionBackendDisabled
	}
	session, ok := s.sessions[token]
	if !ok {
		return Session{}, domain.ErrAuthenticationRequired
	}
	return session, nil
}

// Delete 实现 SessionStore。
func (s *FakeSessionStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	return nil
}

// Count 返回有效会话数量。
func (s *FakeSessionStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// Has 报告指定令牌是否存在。
func (s *FakeSessionStore) Has(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[token]
	return ok
}

// equalFold 是不区分大小写的字符串比较，避免在测试替身里引入额外依赖。
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
