package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RecordOpenAIStreamKeepaliveBytes 只登记原有流心跳字节，不改业务输出计数。
func RecordOpenAIStreamKeepaliveBytes(c *gin.Context, written int) {
	if c == nil || written <= 0 {
		return
	}
	current := 0
	if value, ok := c.Get(openAIStreamKeepaliveBytesContextKey); ok {
		current, _ = value.(int)
	}
	c.Set(openAIStreamKeepaliveBytesContextKey, current+written)
}

// OpenAIStreamClientOutputStarted 保留本地输出标记优先及扣除心跳后的判断。
func OpenAIStreamClientOutputStarted(c *gin.Context, localStarted bool) bool {
	if localStarted {
		return true
	}
	if c == nil || c.Writer == nil {
		return false
	}
	// compact 心跳会提交 HTTP 200，但不属于模型业务输出，不应阻止安全重试。
	return OpenAICompactKeepaliveAdjustedWrittenSize(c) >= 0
}
