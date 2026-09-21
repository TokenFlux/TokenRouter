package service

import (
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
)

// opsCyberPolicyKey 在 gin context 中携带 cyber_policy 命中标记。
// gateway 服务层检测到上游 error.code="cyber_policy" 后设置，handler 收尾时读取并写入风控、用量与 Ops。
const opsCyberPolicyKey = "ops_cyber_policy"

// errOpenAICyberPolicyForwarded 表示 cyber_policy 已按当前端点格式透传给客户端。
// 兼容路径用该哨兵让 handler 进入错误收尾路径，但不再 failover 或补写响应。
var errOpenAICyberPolicyForwarded = errors.New("openai cyber_policy forwarded to client")

// CyberPolicyMark 记录一次 cyber_policy 硬阻断的上游证据。
type CyberPolicyMark struct {
	Code           string
	Message        string
	Body           string
	UpstreamStatus int
	UpstreamInTok  int
	UpstreamOutTok int
}

// MarkOpsCyberPolicy 记录 cyber 标记；同一请求或 WS turn 内首个标记生效。
func MarkOpsCyberPolicy(c *gin.Context, mark CyberPolicyMark) {
	if c == nil {
		return
	}
	if GetOpsCyberPolicy(c) != nil {
		return
	}
	mark.Code = "cyber_policy"
	mark.Message = strings.TrimSpace(mark.Message)
	mark.Body = strings.TrimSpace(mark.Body)
	c.Set(opsCyberPolicyKey, &mark)
}

// GetOpsCyberPolicy 返回 cyber 标记；未命中或已清理时返回 nil。
func GetOpsCyberPolicy(c *gin.Context) *CyberPolicyMark {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(opsCyberPolicyKey); ok {
		if mark, ok := v.(*CyberPolicyMark); ok && mark != nil {
			return mark
		}
	}
	return nil
}

// ClearOpsCyberPolicy 清除 WS turn 级 cyber 标记，避免下一轮复用旧状态。
func ClearOpsCyberPolicy(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(opsCyberPolicyKey, (*CyberPolicyMark)(nil))
}

// markOpenAICyberPolicyEvent 记录 WS 各种错误终止事件中的 cyber_policy 证据。
// 统一入口避免不同传输路径重复维护 usage 与响应体截断逻辑。
func markOpenAICyberPolicyEvent(c *gin.Context, payload []byte, upstreamStatus int, usage *openai.ForwardUsage) bool {
	hit, code, message := upstreamopenai.DetectOpenAICyberPolicy(payload)
	if !hit {
		return false
	}
	mark := CyberPolicyMark{
		Code:           code,
		Message:        message,
		Body:           logredact.TruncateUTF8(string(payload), 4096),
		UpstreamStatus: upstreamStatus,
	}
	if usage != nil {
		mark.UpstreamInTok = usage.InputTokens
		mark.UpstreamOutTok = usage.OutputTokens
	}
	MarkOpsCyberPolicy(c, mark)
	return true
}
