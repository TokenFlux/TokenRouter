package identity_test

import (
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// B01：HTTP 与存储组合测试只在进程启动时设置一次模式。
func TestMain(m *testing.M) {
	time.Local = time.UTC
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
