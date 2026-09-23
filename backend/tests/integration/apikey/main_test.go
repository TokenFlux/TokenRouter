package apikey_test

import (
	"os"
	"testing"
	"time"
)

// 保留原存储测试进程的 UTC 日期边界，在执行测试前完成初始化。
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
