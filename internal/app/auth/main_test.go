package auth_test

import (
	"os"
	"testing"

	"github.com/myonku/project-builder/internal/testsupport"
)

// TestMain 在跑本包测试前获取跨进程数据库咨询锁。
//
// 原因同 internal/testsupport：两个包的集成测试共用同一个物理数据库，
// 必须串行执行，否则会互相清空数据。
func TestMain(m *testing.M) {
	testsupport.HoldDatabaseLock()
	code := m.Run()
	testsupport.ReleaseDatabaseLock()
	os.Exit(code)
}
