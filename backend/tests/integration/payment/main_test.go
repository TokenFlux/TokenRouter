//go:build unit

package payment_test

import (
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 模式在并行 HTTP 契约开始前固定，测试本身不修改全局状态。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	// 测试进程使用 UTC，保证时间夹具不受本地时区影响。
	time.Local = time.UTC
	os.Exit(m.Run())
}
