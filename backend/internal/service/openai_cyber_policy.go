package service

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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

// DetectOpenAICyberPolicy 精确识别上游 cyber_policy 错误。
func DetectOpenAICyberPolicy(payload []byte) (bool, string, string) {
	return detectOpenAICyberPolicy(payload)
}

// detectOpenAICyberPolicy 精确识别 error.code 或 response.error.code 为 cyber_policy 的响应。
func detectOpenAICyberPolicy(payload []byte) (bool, string, string) {
	code := gjson.GetBytes(payload, "error.code").String()
	if code == "" {
		code = gjson.GetBytes(payload, "response.error.code").String()
	}
	if !strings.EqualFold(strings.TrimSpace(code), "cyber_policy") {
		return false, "", ""
	}
	msg := gjson.GetBytes(payload, "error.message").String()
	if msg == "" {
		msg = gjson.GetBytes(payload, "response.error.message").String()
	}
	return true, "cyber_policy", strings.TrimSpace(msg)
}
