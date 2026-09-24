package googleforward_test

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain 在并行测试开始前设置唯一 Gin 模式，避免夹具竞争全局状态。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
