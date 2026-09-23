package httpapi

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
)

// opsCyberPolicyKey 保留原 HTTP/WS turn 标记键；只记录已观测的供应商拒绝证据。
const opsCyberPolicyKey = "ops_cyber_policy"

// MarkOpsCyberPolicy 保持首个标记生效，后续事件不覆盖原用量与失败状态。
func MarkOpsCyberPolicy(c *gin.Context, mark moderationflow.Mark) {
	if c == nil || GetOpsCyberPolicy(c) != nil {
		return
	}
	mark.Code = "cyber_policy"
	mark.Message = strings.TrimSpace(mark.Message)
	mark.Body = strings.TrimSpace(mark.Body)
	c.Set(opsCyberPolicyKey, &mark)
}

// GetOpsCyberPolicy 返回当前请求或 turn 的既有标记，不补造成功或结算资格。
func GetOpsCyberPolicy(c *gin.Context) *moderationflow.Mark {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(opsCyberPolicyKey); ok {
		if mark, ok := value.(*moderationflow.Mark); ok && mark != nil {
			return mark
		}
	}
	return nil
}

// ClearOpsCyberPolicy 保持原带类型 nil 清理行为，防止下一个 WS turn 继承旧标记。
func ClearOpsCyberPolicy(c *gin.Context) {
	if c != nil {
		c.Set(opsCyberPolicyKey, (*moderationflow.Mark)(nil))
	}
}

// MarkOpenAICyberPolicyEvent 由平台适配解析原生事件，HTTP 只保存观测值。
func MarkOpenAICyberPolicyEvent(c *gin.Context, payload []byte, upstreamStatus int, usage *openai.ForwardUsage) bool {
	mark := provider.ParseOpenAICyberPolicyEvent(payload, upstreamStatus, usage)
	if mark == nil {
		return false
	}
	MarkOpsCyberPolicy(c, *mark)
	return true
}
