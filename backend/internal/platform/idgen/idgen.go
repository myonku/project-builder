// Package idgen 是业务对象 ID 的唯一生成入口。
//
// 全局基线约定 4.1：默认使用 UUIDv7（时间有序），统一由本包生成，
// 禁止各模块自行拼接字符串或使用自增整数。
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// NowFunc 返回当前瞬时时间，必须为 UTC。
type NowFunc func() time.Time

// Generator 生成 UUID，支持注入时间来源以便测试可复现。
type Generator struct {
	now NowFunc
	// rnd 是随机字节来源，默认 crypto/rand.Reader。
	rnd io.Reader
	// random 为 true 时始终生成 UUIDv4（用于安全令牌，见全局基线约定 4.2）。
	random bool
}

// NewTimeOrdered 返回生成 UUIDv7 的生成器。
func NewTimeOrdered(now NowFunc) *Generator {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Generator{now: now, rnd: rand.Reader}
}

// NewRandom 返回生成 UUIDv4 的生成器。
//
// 用途：会话标识、下载签名等安全令牌。全局基线约定 4.2 明确要求
// 安全令牌不得使用可预测的 UUIDv7。
func NewRandom() *Generator {
	return &Generator{rnd: rand.Reader, random: true}
}

// New 返回使用系统时间的默认生成器。
func New() *Generator { return NewTimeOrdered(nil) }

// UUID 返回一个标准 36 字符带连字符的小写 UUID 文本。
func (g *Generator) UUID() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(g.rnd, b[:]); err != nil {
		return "", fmt.Errorf("生成随机字节失败: %w", err)
	}
	if g.random {
		// UUIDv4：版本位 0100，变体位 10。
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return format(b), nil
	}

	// UUIDv7：前 48 位为毫秒级 Unix 时间戳，保证时间有序。
	// 剩余 74 位随机，其中版本位 0111、变体位 10。
	ms := uint64(g.now().UTC().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return format(b), nil
}

// MustUUID 在生成失败时 panic，仅用于测试与初始化路径。
func (g *Generator) MustUUID() string {
	id, err := g.UUID()
	if err != nil {
		panic(err)
	}
	return id
}

// Token 返回用于会话标识等安全用途的随机令牌（64 个小写十六进制字符）。
//
// 不使用 UUID：会话令牌需要足够熵且不可预测，见全局基线约定 4.2。
func (g *Generator) Token() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(g.rnd, buf); err != nil {
		return "", fmt.Errorf("生成随机令牌失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// RequestID 返回用于日志与错误信封的请求标识（32 个小写十六进制字符，无连字符）。
func (g *Generator) RequestID() string {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(g.rnd, buf); err != nil {
		// RequestID 是诊断辅助，不应导致请求失败；退化为时间派生值。
		ms := uint64(g.now().UTC().UnixNano())
		for i := 0; i < 8; i++ {
			buf[i] = byte(ms >> (8 * i))
		}
	}
	return hex.EncodeToString(buf)
}

// format 把 16 字节格式化为带连字符的小写 UUID 文本。
func format(b [16]byte) string {
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}

// IsUUID 报告字符串是否为合法的 36 字符带连字符十六进制 UUID。
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
