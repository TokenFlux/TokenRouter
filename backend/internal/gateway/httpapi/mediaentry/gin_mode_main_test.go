package mediaentry

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// 模式在测试开始前只初始化一次，并行测试不写 Gin 全局状态。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
