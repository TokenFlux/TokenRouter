package team_test

import (
	"os"
	"testing"
	"time"
)

// TestMain 保留原存储测试进程的 UTC 日历，避免将宿主机 Local 名称传入 PostgreSQL。
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
