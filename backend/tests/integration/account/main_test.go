package account_test

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// B01：测试进程启动时统一设置 Gin 模式，保留测试中的并发行为。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(runAccountTests(m))
}

var runAccountTests = func(m *testing.M) int { return m.Run() }
