// HTTP 客户端识别保持 UA 快路径，不增加请求体读取。
package httpapi

import (
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/gin-gonic/gin"
)

// ClientDetection 只携带识别结果；请求许可与平台默认值仍由相应用例裁决。
type ClientDetection struct {
	ClaudeCode bool
	Version    string
}

var claudeCodeValidator = clientmeta.NewClaudeCodeValidator()

// DetectClaudeCodeRequest 返回显式识别结果，旧 context 写入由兼容入口完成。
func DetectClaudeCodeRequest(c *gin.Context, body []byte, parsedReq *requeststate.ParsedRequest, probe bool) ClientDetection {
	if c == nil || c.Request == nil {
		return ClientDetection{}
	}
	ua := c.GetHeader("User-Agent")
	// Fast path：非 Claude CLI UA 直接判定 false，避免热路径二次 JSON 反序列化。
	if !claudeCodeValidator.ValidateUserAgent(ua) {
		return ClientDetection{}
	}

	isClaudeCode := false
	if !strings.Contains(c.Request.URL.Path, "messages") {
		// 与 Validate 行为一致：非 messages 路径 UA 命中即可视为 Claude Code 客户端。
		isClaudeCode = true
	} else {
		// 仅在确认为 Claude CLI 且 messages 路径时再做 body 解析。
		bodyMap := ClaudeCodeBodyMapFromParsedRequest(parsedReq)
		if bodyMap == nil && len(body) > 0 {
			_ = json.Unmarshal(body, &bodyMap)
		}
		isClaudeCode = claudeCodeValidator.Validate(clientmeta.ClaudeCodeValidationInput{Path: c.Request.URL.Path, UserAgent: ua, XApp: c.GetHeader("X-App"), AnthropicBeta: c.GetHeader("anthropic-beta"), AnthropicVersion: c.GetHeader("anthropic-version"), MaxTokensOneHaiku: probe}, bodyMap)
	}

	result := ClientDetection{ClaudeCode: isClaudeCode}
	if isClaudeCode {
		result.Version = claudeCodeValidator.ExtractVersion(ua)
	}
	return result
}

func ClaudeCodeBodyMapFromParsedRequest(parsedReq *requeststate.ParsedRequest) map[string]any {
	if parsedReq == nil {
		return nil
	}
	bodyMap := map[string]any{
		"model": parsedReq.Model,
	}
	if parsedReq.HasSystem {
		if system, ok := parsedReq.SystemValue(); ok {
			bodyMap["system"] = system
		} else {
			bodyMap["system"] = nil
		}
	}
	if parsedReq.MetadataUserID != "" {
		bodyMap["metadata"] = map[string]any{"user_id": parsedReq.MetadataUserID}
	}
	return bodyMap
}
