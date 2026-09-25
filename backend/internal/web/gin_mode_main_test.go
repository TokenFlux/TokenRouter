package web

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// 测试进程运行前统一设置模式，测试及并行夹具不再写 Gin 全局变量。
func TestMain(m *testing.M) { gin.SetMode(gin.TestMode); os.Exit(m.Run()) }
