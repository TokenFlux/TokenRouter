package httpapi

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// 测试启动前统一设置模式，保留各协议测试的并行执行。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
