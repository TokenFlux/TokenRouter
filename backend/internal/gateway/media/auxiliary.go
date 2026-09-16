// 辅助端点的请求与完成规则只处理显式值；原生协议执行由 Adapter 接入。
package media

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/tidwall/gjson"
)

// RealtimeAudioUsage 仅结算真实观察到音频的正时长会话。
func RealtimeAudioUsage(elapsed time.Duration, audioObserved bool) *protocol.AudioUsage {
	if !audioObserved || elapsed <= 0 {
		return nil
	}
	return &protocol.AudioUsage{Mode: "realtime", DurationOrUnits: elapsed.Minutes()}
}

// TTSInputText 保留 input/text/prompt 的首个字符串优先级，包括显式空字符串。
func TTSInputText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"input", "text", "prompt"} {
		if raw, ok := payload[key]; ok {
			// JSON null 不能当作字符串，从而保留后续字段回退。
			if len(raw) == 0 || raw[0] != '"' {
				continue
			}
			var value string
			if json.Unmarshal(raw, &value) == nil {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

// AlphaEndpointUnsupported 只对 API Key 缺少独立搜索端点的响应允许换号。
func AlphaEndpointUnsupported(apiKey bool, statusCode int) bool {
	return apiKey && (statusCode == 404 || statusCode == 405)
}

// AlphaAccountErrorSideEffects 保留工具端点拒绝与账号全局健康之间的边界。
func AlphaAccountErrorSideEffects(statusCode int) bool {
	switch statusCode {
	case 401, 404, 405:
		return false
	default:
		return true
	}
}

// RequiredModel 只读取已经在原位置取到的报文，不触发请求体读取。
func RequiredModel(body []byte, trim bool) (string, bool) {
	value := gjson.GetBytes(body, "model")
	if !value.Exists() || value.Type != gjson.String || strings.TrimSpace(value.String()) == "" {
		return "", false
	}
	if trim {
		return strings.TrimSpace(value.String()), true
	}
	return value.String(), true
}

// IsVideoCreate 区分异步创建与查询，不能在创建成功时扣费。
func IsVideoCreate(endpoint string) bool {
	switch endpoint {
	case "videos_generations", "videos_edits", "videos_extensions":
		return true
	default:
		return false
	}
}

// RecordImmediateImages 只允许带有实际图片产出的即时生成完成事件。
func RecordImmediateImages(endpoint, requestModel string, imageCount int) bool {
	if strings.TrimSpace(requestModel) == "" || imageCount <= 0 {
		return false
	}
	return endpoint == "images_generations" || endpoint == "images_edits"
}

// RewriteMappedMediaBody 保留未命中映射时原字节与 multipart content-type。
func RewriteMappedMediaBody(body []byte, contentType string, mapped bool, model string, rewrite func([]byte, string, string) ([]byte, string, error)) ([]byte, string, error) {
	if !mapped || strings.TrimSpace(model) == "" {
		return body, contentType, nil
	}
	return rewrite(body, contentType, model)
}
