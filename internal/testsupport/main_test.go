package testsupport

import (
	"os"
	"testing"
)

// TestMain 在跑本包测试前获取跨进程数据库咨询锁。
//
// Go 默认为每个包启动独立测试二进制并并行执行，而本包与 internal/app/auth 的
// 集成测试共用同一个 PostgreSQL 数据库。咨询锁把两者串行化，
// 避免互相 TRUNCATE 导致"单独跑通过、一起跑失败"的假故障。
func TestMain(m *testing.M) {
	HoldDatabaseLock()
	code := m.Run()
	ReleaseDatabaseLock()
	os.Exit(code)
}
