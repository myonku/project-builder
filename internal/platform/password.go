package platform

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2 参数。默认值对齐 OWASP 对 Argon2id 的建议（19 MiB、t=2、p=1）；
// 这里取更高的内存与并行度，作为内部系统的安全余量。
//
// 摘要格式与 Python 版本的 argon2-cffi 一致（PHC 字符串），
// 便于两边在过渡期互相校验同一批摘要。
type Argon2Params struct {
	// Memory 单位 KiB。
	Memory uint32
	// Iterations 时间代价。
	Iterations uint32
	// Parallelism 并行度。
	Parallelism uint8
	// SaltLength 盐长度（字节）。
	SaltLength uint32
	// KeyLength 摘要长度（字节）。
	KeyLength uint32
}

// DefaultArgon2Params 返回默认参数。
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{
		Memory:      64 * 1024, // 64 MiB
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// argon2Version 是当前 Argon2 版本号，写入 PHC 字符串的 v 段。
const argon2Version = argon2.Version

// ErrInvalidHashFormat 表示摘要不是可解析的 PHC 字符串。
var ErrInvalidHashFormat = errors.New("密码摘要格式无效")

// PasswordHasher 负责密码摘要与校验。
//
// 全局基线约定 8.2：密码必须使用专用的自适应摘要算法，且只由认证模块使用；
// 摘要不得出现在日志、响应或错误信息中。
type PasswordHasher struct {
	params Argon2Params
}

// NewPasswordHasher 使用默认参数构造摘要器。
func NewPasswordHasher() *PasswordHasher {
	return &PasswordHasher{params: DefaultArgon2Params()}
}

// NewPasswordHasherWith 使用自定义参数构造摘要器（测试用低成本参数）。
func NewPasswordHasherWith(params Argon2Params) *PasswordHasher {
	return &PasswordHasher{params: params}
}

// Hash 生成 Argon2id PHC 格式摘要。
func (h *PasswordHasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码盐失败: %w", err)
	}

	key := argon2.IDKey(
		[]byte(password),
		salt,
		h.params.Iterations,
		h.params.Memory,
		h.params.Parallelism,
		h.params.KeyLength,
	)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		h.params.Memory,
		h.params.Iterations,
		h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify 校验明文密码是否匹配摘要。
//
// 使用 subtle.ConstantTimeCompare 做常数时间比较，避免通过响应时间侧信道推断摘要。
// 摘要无法解析时返回 false 与错误，调用方应记录内部日志但对外统一返回凭据错误。
func (h *PasswordHasher) Verify(password, encodedHash string) (bool, error) {
	params, salt, want, err := decodeArgon2Hash(encodedHash)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.Memory,
		params.Parallelism,
		uint32(len(want)),
	)

	if subtle.ConstantTimeCompare(got, want) == 1 {
		return true, nil
	}
	return false, nil
}

// NeedsRehash 报告现有摘要是否使用了弱于当前配置的参数。
//
// 用途：登录成功后按需升级老摘要，无需强制用户改密。
func (h *PasswordHasher) NeedsRehash(encodedHash string) bool {
	params, _, key, err := decodeArgon2Hash(encodedHash)
	if err != nil {
		return true
	}
	return params.Memory < h.params.Memory ||
		params.Iterations < h.params.Iterations ||
		params.Parallelism < h.params.Parallelism ||
		uint32(len(key)) < h.params.KeyLength
}

// decodeArgon2Hash 解析 PHC 格式的 Argon2id 摘要。
func decodeArgon2Hash(encodedHash string) (Argon2Params, []byte, []byte, error) {
	var params Argon2Params

	parts := strings.Split(encodedHash, "$")
	// 形如 ["", "argon2id", "v=19", "m=65536,t=3,p=2", "<salt>", "<key>"]
	if len(parts) != 6 || parts[0] != "" {
		return params, nil, nil, ErrInvalidHashFormat
	}
	if parts[1] != "argon2id" {
		return params, nil, nil, fmt.Errorf("%w: 不支持的算法 %q", ErrInvalidHashFormat, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return params, nil, nil, fmt.Errorf("%w: 版本段无效", ErrInvalidHashFormat)
	}
	if version != argon2Version {
		return params, nil, nil, fmt.Errorf("%w: 不支持的版本 %d", ErrInvalidHashFormat, version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&params.Memory, &params.Iterations, &params.Parallelism); err != nil {
		return params, nil, nil, fmt.Errorf("%w: 参数段无效", ErrInvalidHashFormat)
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return params, nil, nil, fmt.Errorf("%w: 盐段不是合法 base64", ErrInvalidHashFormat)
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return params, nil, nil, fmt.Errorf("%w: 摘要段不是合法 base64", ErrInvalidHashFormat)
	}
	if len(salt) == 0 || len(key) == 0 {
		return params, nil, nil, fmt.Errorf("%w: 盐或摘要为空", ErrInvalidHashFormat)
	}

	return params, salt, key, nil
}
