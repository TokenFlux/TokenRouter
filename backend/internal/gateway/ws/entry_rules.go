package ws

import (
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// EntryFallbackSeed 保留无显式会话标识时的旧隔离键格式。
func EntryFallbackSeed(userID, keyID int64, groupID *int64) string {
	var group int64
	if groupID != nil {
		group = *groupID
	}
	return fmt.Sprintf("openai_ws_ingress:%d:%d:%d", group, userID, keyID)
}
func entrySeedHash(seed string) string {
	value, _ := scheduler.DeriveSessionHashes(seed)
	return value
}

// EntryNextAttemptMessage 只在存在当前 turn 完整重放时换号，避免重发已完成轮次。
func EntryNextAttemptMessage(current, retry []byte, currentTurn bool) ([]byte, bool) {
	if !currentTurn {
		return append([]byte(nil), current...), true
	}
	if len(retry) == 0 {
		return nil, false
	}
	return append([]byte(nil), retry...), true
}

// EntryBillingModel 保留渠道价卡覆盖的原优先级。
func EntryBillingModel(result *ForwardResult, mapping routing.ChannelMappingResult, requested, upstream string) string {
	model := ""
	if result != nil {
		model = strings.TrimSpace(result.BillingModel)
	}
	if model == "" {
		model = strings.TrimSpace(upstream)
	}
	if model == "" {
		model = strings.TrimSpace(requested)
	}
	requested = strings.TrimSpace(requested)
	switch mapping.BillingModelSource {
	case routing.BillingModelSourceRequested:
		if requested != "" {
			model = requested
		}
	case routing.BillingModelSourceChannelMapped:
		if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" && mapped != requested {
			model = mapped
		}
	}
	return model
}
func entrySucceeded(r *ForwardResult) bool {
	if r == nil || !r.OpenAIWSMode || r.UpstreamTerminalEvent == "" {
		return true
	}
	return r.UpstreamTerminalEvent == "response.completed" || r.UpstreamTerminalEvent == "response.done"
}

// ErrEntryLocalRoutingRejected 只标记本地账号资格拒绝，不把它记作上游故障。
var ErrEntryLocalRoutingRejected = errors.New("local websocket routing rejected")

func EntryLocalRoutingReason(model string) string {
	return fmt.Sprintf("model %s is not available for this websocket channel or account", strings.TrimSpace(model))
}
func EntryLocalRoutingCause(err error) error {
	return fmt.Errorf("%w: %w", ErrEntryLocalRoutingRejected, err)
}
func EntryShouldReportFailure(err error) bool {
	if err == nil || errors.Is(err, ErrEntryLocalRoutingRejected) || IsSessionPreemptedError(err) {
		return false
	}
	var policy *routing.ReasoningEffortOverLimitError
	return !errors.As(err, &policy)
}
