// Package clock 提供可注入的时钟。
//
// 全局基线约定 6.3 要求"业务代码不得直接散落调用系统时间函数"，测试必须能注入固定时间。
// 本包被 internal/domain 与各业务模块共同依赖，因此只使用标准库。
package clock

import "time"

// Clock 是时间来源抽象。
type Clock interface {
	// Now 返回当前瞬时时间。实现必须返回 UTC。
	Now() time.Time
}

// System 是使用操作系统时间的实时时钟。
type System struct{}

// NewSystem 返回实时时钟。
func NewSystem() System { return System{} }

// Now 返回当前 UTC 时间。
func (System) Now() time.Time { return time.Now().UTC() }

// Fixed 是返回固定时间的测试时钟。零值表示未显式设置，需通过 NewFixed 构造。
type Fixed struct {
	instant time.Time
}

// NewFixed 基于给定时间构造固定时钟，统一转为 UTC。
func NewFixed(t time.Time) *Fixed { return &Fixed{instant: t.UTC()} }

// Now 返回构造时的固定时间。
func (f *Fixed) Now() time.Time { return f.instant }

// Set 调整固定时钟的时间，便于在单个测试内推进时间。
func (f *Fixed) Set(t time.Time) { f.instant = t.UTC() }

// Advance 将固定时钟向前推进 d。
func (f *Fixed) Advance(d time.Duration) { f.instant = f.instant.Add(d) }
