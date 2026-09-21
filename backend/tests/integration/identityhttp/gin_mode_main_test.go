package identityhttp_test

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain 在并行用例开始前统一初始化 Gin 模式。
func TestMain(m *testing.M) { gin.SetMode(gin.TestMode); os.Exit(m.Run()) }
