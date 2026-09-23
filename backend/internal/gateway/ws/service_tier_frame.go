package ws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// applyOpenAIFastPolicyToWSResponseCreate 针对单个 client -> upstream WebSocket
// 帧评估 OpenAI fast policy，该帧的顶层 "type" 必须是 "response.create"。
// 该函数镜像 HTTP 侧 applyOpenAIFastPolicyToBody 的契约，但作用于
// Realtime/Responses WS payload：
//
//   - pass：保留 service_tier，并将 "fast" 等别名归一化为 "priority"
//   - filter：返回删除顶层 service_tier 的副本
//   - force_priority：保留 service_tier，并将其强制改写为 "priority"
//   - block：返回 (frame, *OpenAIFastBlockedError)
//
// 所有帧先校验 Ultra；只有 "type" 字段严格等于 "response.create" 的帧
// 会继续执行 fast policy 检查或修改。其它帧类型（包括空字符串）
// 在通过 Ultra 校验后原样透传。OpenAI Realtime client-event 规范要求设置
// "type"，因此空 type 被视为畸形帧，本层不拦截，由上游负责拒绝。
//
// service_tier 位于 response.create 顶层，与 Responses HTTP body 形态一致
// （参见 openai_gateway_chat_completions.go:304、requeststate.ExtractOpenAIServiceTierFromBody
// 以及 openai_ws_forwarder_ingress_session_test.go:402 的测试样例）。因此这里只需
// 检查或剥离顶层字段；当前 schema 没有嵌套形式。
//
// 调用方负责传入用于上游请求的 model；该 helper 不会重新推导。
func ApplyServiceTierFrame(frame []byte, model string, input tierpolicy.DecisionInput) ([]byte, *tierpolicy.BlockedError, error) {
	if len(frame) == 0 {
		return frame, nil, nil
	}
	if !gjson.ValidBytes(frame) {
		return frame, nil, nil
	}
	// WS 会话允许逐帧切换参数，因此每个客户端帧都必须在上游转发前拒绝 Ultra。
	if err := requeststate.ValidateOpenAIReasoningEffort(frame, model); err != nil {
		return frame, nil, err
	}
	frameType := strings.TrimSpace(gjson.GetBytes(frame, "type").String())
	// Strict match: only response.create is policy-checked. Empty / other
	// types pass through untouched so we never accidentally strip fields
	// from response.cancel, conversation.item.create, or any future
	// client-event the spec adds. The Realtime spec requires "type" on
	// every client event, so an empty type is malformed input — let the
	// upstream reject it rather than guessing at our layer.
	if frameType != "response.create" {
		return frame, nil, nil
	}
	tierResult := gjson.GetBytes(frame, "service_tier")
	decision := tierpolicy.Resolve(input, tierResult.String(), tierResult.Exists())
	if decision.Blocked != nil {
		return frame, decision.Blocked, nil
	}
	if decision.DeleteField {
		trimmed, err := sjson.DeleteBytes(frame, "service_tier")
		if err != nil {
			return frame, nil, fmt.Errorf("strip service_tier from ws frame: %w", err)
		}
		return trimmed, nil, nil
	}
	if decision.Tier != "" && (!tierResult.Exists() || decision.Tier != tierResult.String()) {
		updated, err := sjson.SetBytes(frame, "service_tier", decision.Tier)
		if err != nil {
			return frame, nil, fmt.Errorf("apply service_tier in ws frame: %w", err)
		}
		return updated, nil, nil
	}
	return frame, nil, nil
}

// newOpenAIFastPolicyWSEventID returns a Realtime-style event_id for a
// server-emitted error event. Matches the loose "evt_<rand>" convention used
// by upstream Realtime servers; the exact value is not load-bearing and is
// only required for client-side log correlation. We reuse the existing
// google/uuid dependency rather than pulling a new one.
func newOpenAIFastPolicyWSEventID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		// Extremely unlikely; fall back to a fixed prefix so the field is
		// still non-empty and the schema stays self-consistent.
		return "evt_openai_fast_policy"
	}
	// Strip dashes so it visually matches "evt_<hex>" rather than UUID v4
	// canonical form, mirroring what real Realtime traces look like.
	return "evt_" + strings.ReplaceAll(id.String(), "-", "")
}

// BuildFastPolicyBlockedEvent renders an OpenAI Realtime/Responses
// style "error" event payload for a request blocked by the OpenAI fast
// policy. The shape mirrors Realtime error events as observed in upstream
// traces and per the spec's server "error" event:
//
//	{
//	  "event_id": "evt_<random>",
//	  "type": "error",
//	  "error": {
//	    "type": "invalid_request_error",
//	    "code": "policy_violation",
//	    "message": "..."
//	  }
//	}
//
// event_id lets clients correlate the rejection in their logs; "code" gives
// programmatic clients a stable identifier (HTTP-side equivalent is the
// 403 permission_error JSON body).
func BuildFastPolicyBlockedEvent(err *tierpolicy.BlockedError) []byte {
	if err == nil {
		return nil
	}
	eventID := newOpenAIFastPolicyWSEventID()
	payload, mErr := json.Marshal(map[string]any{
		"event_id": eventID,
		"type":     "error",
		"error": map[string]any{
			"type":    "invalid_request_error",
			"code":    "policy_violation",
			"message": err.Message,
		},
	})
	if mErr != nil {
		// Fallback to a minimal hand-rolled payload; Marshal of the literal
		// shape above should never fail in practice.
		return []byte(`{"event_id":"` + eventID + `","type":"error","error":{"type":"invalid_request_error","code":"policy_violation","message":"openai fast policy blocked this request"}}`)
	}
	return payload
}
