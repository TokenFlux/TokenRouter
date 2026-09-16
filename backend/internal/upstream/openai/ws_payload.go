// 平台续接报文只处理每轮输入，账号选择、会话缓存与入站重试由调用方拥有。
package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unsafe"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func SetOpenAIWSTurnMetadata(payload map[string]any, turnMetadata string) {
	if len(payload) == 0 {
		return
	}
	metadata := strings.TrimSpace(turnMetadata)
	if metadata == "" {
		return
	}

	switch existing := payload["client_metadata"].(type) {
	case map[string]any:
		existing[WSTurnMetadataHeader] = metadata
		payload["client_metadata"] = existing
	case map[string]string:
		next := make(map[string]any, len(existing)+1)
		for k, v := range existing {
			next[k] = v
		}
		next[WSTurnMetadataHeader] = metadata
		payload["client_metadata"] = next
	default:
		payload["client_metadata"] = map[string]any{
			WSTurnMetadataHeader: metadata,
		}
	}
}

func ShouldForceNewConnOnStoreDisabled(mode, lastFailureReason string) bool {
	switch mode {
	case "off":
		return false
	case "adaptive":
		reason := strings.TrimPrefix(strings.TrimSpace(lastFailureReason), "prewarm_")
		switch reason {
		case "policy_violation", "message_too_big", "auth_failed", "write_request", "write":
			return true
		default:
			return false
		}
	default:
		return true
	}
}

func DropPreviousResponseIDFromRawPayload(payload []byte) ([]byte, bool, error) {
	return DropPreviousResponseIDFromRawPayloadWithDeleteFn(payload, sjson.DeleteBytes)
}

func DropPreviousResponseIDFromRawPayloadWithDeleteFn(
	payload []byte,
	deleteFn func([]byte, string) ([]byte, error),
) ([]byte, bool, error) {
	if len(payload) == 0 {
		return payload, false, nil
	}
	if !gjson.GetBytes(payload, "previous_response_id").Exists() {
		return payload, false, nil
	}
	if deleteFn == nil {
		deleteFn = sjson.DeleteBytes
	}

	updated := payload
	for i := 0; i < WSMaxPrevResponseIDDeletePasses &&
		gjson.GetBytes(updated, "previous_response_id").Exists(); i++ {
		next, err := deleteFn(updated, "previous_response_id")
		if err != nil {
			return payload, false, err
		}
		updated = next
	}
	return updated, !gjson.GetBytes(updated, "previous_response_id").Exists(), nil
}

func SetPreviousResponseIDToRawPayload(payload []byte, previousResponseID string) ([]byte, error) {
	normalizedPrevID := strings.TrimSpace(previousResponseID)
	if len(payload) == 0 || normalizedPrevID == "" {
		return payload, nil
	}
	updated, err := sjson.SetBytes(payload, "previous_response_id", normalizedPrevID)
	if err == nil {
		return updated, nil
	}

	var reqBody map[string]any
	if unmarshalErr := json.Unmarshal(payload, &reqBody); unmarshalErr != nil {
		return nil, err
	}
	reqBody["previous_response_id"] = normalizedPrevID
	rebuilt, marshalErr := json.Marshal(reqBody)
	if marshalErr != nil {
		return nil, marshalErr
	}
	return rebuilt, nil
}

func ShouldInferIngressFunctionCallOutputPreviousResponseID(
	storeDisabled bool,
	turn int,
	signals wire.ToolContinuationSignals,
	currentPreviousResponseID string,
	expectedPreviousResponseID string,
) bool {
	if !storeDisabled || turn <= 1 || !signals.HasFunctionCallOutput {
		return false
	}
	if strings.TrimSpace(currentPreviousResponseID) != "" {
		return false
	}
	if signals.HasFunctionCallOutputMissingCallID {
		return false
	}
	// If the client already sent the actual tool-call context, treat this as
	// a full replay / self-contained continuation payload rather than
	// downgrading it into an inferred delta continuation. item_reference alone
	// is not enough on the store=false WS path: it still needs a valid prior
	// response anchor so upstream can resolve the referenced function_call.
	if signals.HasToolCallContext {
		return false
	}
	return strings.TrimSpace(expectedPreviousResponseID) != ""
}

func AlignStoreDisabledPreviousResponseID(
	payload []byte,
	expectedPreviousResponseID string,
) ([]byte, bool, error) {
	if len(payload) == 0 {
		return payload, false, nil
	}
	expected := strings.TrimSpace(expectedPreviousResponseID)
	if expected == "" {
		return payload, false, nil
	}
	current := WSPayloadStringFromRaw(payload, "previous_response_id")
	if current == "" || current == expected {
		return payload, false, nil
	}

	withoutPrev, removed, dropErr := DropPreviousResponseIDFromRawPayload(payload)
	if dropErr != nil {
		return payload, false, dropErr
	}
	if !removed {
		return payload, false, nil
	}
	updated, setErr := SetPreviousResponseIDToRawPayload(withoutPrev, expected)
	if setErr != nil {
		return payload, false, setErr
	}
	return updated, true, nil
}

// CombineOpenAIWSReplayItems 合并历史与增量为新头数组，正文共享不复制。
func CombineOpenAIWSReplayItems(history, delta []json.RawMessage) []json.RawMessage {
	if len(delta) == 0 {
		return history
	}
	combined := make([]json.RawMessage, 0, len(history)+len(delta))
	combined = append(combined, history...)
	return append(combined, delta...)
}

// OpenAIWSPayloadStringView 返回与 payload 共享底层数组的零拷贝 string 视图，
// 供 gjson.Get 使用（gjson.GetBytes 会整段复制结果 Raw，对 input 这类占
// payload 主体的字段是每次 O(payload) 分配）。调用方必须保证 payload 在结果
// 存活期间不可变（replay 所有权不变式）。
func OpenAIWSPayloadStringView(payload []byte) string {
	return unsafe.String(unsafe.SliceData(payload), len(payload))
}

// OpenAIWSRawMessageFromResult 优先返回 parent 的子切片（gjson 值零拷贝共享），
// Index 不可用时回退为复制。共享要求 parent 遵守上面的不可变约定。
func OpenAIWSRawMessageFromResult(parent []byte, value gjson.Result) json.RawMessage {
	idx := value.Index
	if idx > 0 && idx+len(value.Raw) <= len(parent) && string(parent[idx:idx+len(value.Raw)]) == value.Raw {
		return json.RawMessage(parent[idx : idx+len(value.Raw)])
	}
	return json.RawMessage(value.Raw)
}

func NormalizeOpenAIWSJSONForCompare(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("json is empty")
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func NormalizeOpenAIWSJSONForCompareOrRaw(raw []byte) []byte {
	normalized, err := NormalizeOpenAIWSJSONForCompare(raw)
	if err != nil {
		return bytes.TrimSpace(raw)
	}
	return normalized
}

func NormalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("payload is empty")
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	delete(decoded, "input")
	delete(decoded, "previous_response_id")
	// Codex 会在每次 response.create 时更新传输元数据，这些字段不改变 previous_response_id 引用的上下文。
	delete(decoded, "client_metadata")
	delete(decoded, "stream_options")
	// 官方 Codex 使用 generate=false 预热连接，后续业务请求会省略该字段；只归一化 false，保留 true 的语义变化。
	if generate, ok := decoded["generate"].(bool); ok && !generate {
		delete(decoded, "generate")
	}
	return json.Marshal(decoded)
}

// OpenAIWSExtractNormalizedInputSequence 拆出 input 序列。返回的正文尽可能与
// payload 共享底层数组（零拷贝），受 replay 所有权不变式保护。
func OpenAIWSExtractNormalizedInputSequence(payload []byte) ([]json.RawMessage, bool, error) {
	if len(payload) == 0 {
		return nil, false, nil
	}
	inputValue := gjson.Get(OpenAIWSPayloadStringView(payload), "input")
	if !inputValue.Exists() {
		return nil, false, nil
	}
	if inputValue.Type == gjson.JSON {
		if inputValue.IsArray() {
			// gjson 宽容解析；数组整体先做零分配合法性校验，避免把断裂
			// JSON 塞进 replay 历史。
			arrayRaw := OpenAIWSRawMessageFromResult(payload, inputValue)
			if !json.Valid(arrayRaw) {
				return nil, true, errors.New("input array json is invalid")
			}
			elems := inputValue.Array()
			items := make([]json.RawMessage, 0, len(elems))
			for _, elem := range elems {
				items = append(items, OpenAIWSRawMessageFromResult(payload, elem))
			}
			return items, true, nil
		}
		return []json.RawMessage{OpenAIWSRawMessageFromResult(payload, inputValue)}, true, nil
	}
	if inputValue.Type == gjson.String {
		encoded, _ := json.Marshal(inputValue.String())
		return []json.RawMessage{encoded}, true, nil
	}
	return []json.RawMessage{OpenAIWSRawMessageFromResult(payload, inputValue)}, true, nil
}

func OpenAIWSInputIsPrefixExtended(previousPayload, currentPayload []byte) (bool, error) {
	previousItems, previousExists, prevErr := OpenAIWSExtractNormalizedInputSequence(previousPayload)
	if prevErr != nil {
		return false, prevErr
	}
	currentItems, currentExists, currentErr := OpenAIWSExtractNormalizedInputSequence(currentPayload)
	if currentErr != nil {
		return false, currentErr
	}
	if !previousExists && !currentExists {
		return true, nil
	}
	if !previousExists {
		return len(currentItems) == 0, nil
	}
	if !currentExists {
		return len(previousItems) == 0, nil
	}
	if len(currentItems) < len(previousItems) {
		return false, nil
	}

	for idx := range previousItems {
		previousNormalized := NormalizeOpenAIWSJSONForCompareOrRaw(previousItems[idx])
		currentNormalized := NormalizeOpenAIWSJSONForCompareOrRaw(currentItems[idx])
		if !bytes.Equal(previousNormalized, currentNormalized) {
			return false, nil
		}
	}
	return true, nil
}

func OpenAIWSRawItemsHasPrefix(items []json.RawMessage, prefix []json.RawMessage) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(items) < len(prefix) {
		return false
	}
	for idx := range prefix {
		// 快路径：客户端逐字节重发历史时直接比较，避免整轮历史的解码/再编码。
		if bytes.Equal(bytes.TrimSpace(prefix[idx]), bytes.TrimSpace(items[idx])) {
			continue
		}
		previousNormalized := NormalizeOpenAIWSJSONForCompareOrRaw(prefix[idx])
		currentNormalized := NormalizeOpenAIWSJSONForCompareOrRaw(items[idx])
		if !bytes.Equal(previousNormalized, currentNormalized) {
			return false
		}
	}
	return true
}

func OpenAIWSRawItemsHasFunctionCallOutput(items []json.RawMessage) bool {
	for _, item := range items {
		if wire.IsCodexToolCallOutputItemType(gjson.GetBytes(item, "type").String()) {
			return true
		}
	}
	return false
}

// OpenAIWSRawItemsHaveToolCallContextForOutputs 判断工具输出是否带有对应的工具调用上下文。
func OpenAIWSRawItemsHaveToolCallContextForOutputs(items []json.RawMessage) bool {
	if len(items) == 0 {
		return false
	}
	contextCallIDs := make(map[string]struct{})
	outputCallIDs := make(map[string]struct{})
	for _, item := range items {
		itemType := gjson.GetBytes(item, "type").String()
		callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
		switch {
		case wire.IsCodexToolCallContextItemType(itemType):
			if callID != "" {
				contextCallIDs[callID] = struct{}{}
			}
		case wire.IsCodexToolCallOutputItemType(itemType):
			if callID == "" {
				return false
			}
			outputCallIDs[callID] = struct{}{}
		}
	}
	if len(outputCallIDs) == 0 || len(contextCallIDs) == 0 {
		return false
	}
	for callID := range outputCallIDs {
		if _, ok := contextCallIDs[callID]; !ok {
			return false
		}
	}
	return true
}

// SanitizeOpenAIWSHistoricalReplayToolCalls 清理历史中没有对应输出的工具调用，
// 避免剥离 previous_response_id 后向上游重放无法闭合的旧调用上下文。
// SanitizeOpenAIWSHistoricalReplayToolCalls 返回的新头数组与 previousItems 共享正文。
func SanitizeOpenAIWSHistoricalReplayToolCalls(
	previousItems []json.RawMessage,
	currentItems []json.RawMessage,
) []json.RawMessage {
	if len(previousItems) == 0 {
		return previousItems
	}
	outputCallIDs := make(map[string]struct{})
	collectOutputCallIDs := func(items []json.RawMessage) {
		for _, item := range items {
			if !wire.IsCodexToolCallOutputItemType(gjson.GetBytes(item, "type").String()) {
				continue
			}
			if callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String()); callID != "" {
				outputCallIDs[callID] = struct{}{}
			}
		}
	}
	collectOutputCallIDs(previousItems)
	collectOutputCallIDs(currentItems)

	sanitized := make([]json.RawMessage, 0, len(previousItems))
	for _, item := range previousItems {
		if wire.IsCodexToolCallContextItemType(gjson.GetBytes(item, "type").String()) {
			callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
			if _, paired := outputCallIDs[callID]; !paired {
				continue
			}
		}
		sanitized = append(sanitized, item)
	}
	return sanitized
}

// OpenAIWSRawPayloadHasToolCallOutput 判断 response.create payload 的 input 是否包含工具输出。
func OpenAIWSRawPayloadHasToolCallOutput(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	input := gjson.Get(OpenAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return false
	}
	if input.IsArray() {
		for _, item := range input.Array() {
			if wire.IsCodexToolCallOutputItemType(item.Get("type").String()) {
				return true
			}
		}
		return false
	}
	if input.Type == gjson.JSON {
		return wire.IsCodexToolCallOutputItemType(input.Get("type").String())
	}
	return false
}

// BuildOpenAIWSReplayInputSequenceFromItems 基于已解析的当前 turn input 构建
// replay 序列。返回序列的正文与 previousFullInput/currentItems 共享所有权
// （见 CombineOpenAIWSReplayItems 上方的所有权不变式），头数组可能直接转移自
// currentItems。
func BuildOpenAIWSReplayInputSequenceFromItems(
	previousFullInput []json.RawMessage,
	previousFullInputExists bool,
	currentItems []json.RawMessage,
	currentExists bool,
	hasPreviousResponseID bool,
) ([]json.RawMessage, bool) {
	if !hasPreviousResponseID || !previousFullInputExists {
		return currentItems, currentExists
	}
	previousFullInput = SanitizeOpenAIWSHistoricalReplayToolCalls(previousFullInput, currentItems)
	if !currentExists || len(currentItems) == 0 {
		return previousFullInput, true
	}
	if OpenAIWSRawItemsHasPrefix(currentItems, previousFullInput) {
		return currentItems, true
	}
	merged := make([]json.RawMessage, 0, len(previousFullInput)+len(currentItems))
	merged = append(merged, previousFullInput...)
	merged = append(merged, currentItems...)
	return merged, true
}

func BuildOpenAIWSReplayInputSequence(
	previousFullInput []json.RawMessage,
	previousFullInputExists bool,
	currentPayload []byte,
	hasPreviousResponseID bool,
) ([]json.RawMessage, bool, error) {
	currentItems, currentExists, currentErr := OpenAIWSExtractNormalizedInputSequence(currentPayload)
	if currentErr != nil {
		return nil, false, currentErr
	}
	items, exists := BuildOpenAIWSReplayInputSequenceFromItems(
		previousFullInput,
		previousFullInputExists,
		currentItems,
		currentExists,
		hasPreviousResponseID,
	)
	return items, exists, nil
}

func SetOpenAIWSPayloadInputSequence(
	payload []byte,
	fullInput []json.RawMessage,
	fullInputExists bool,
) ([]byte, error) {
	if !fullInputExists {
		return payload, nil
	}
	// Preserve [] vs null semantics when input exists but is empty.
	inputForMarshal := fullInput
	if inputForMarshal == nil {
		inputForMarshal = []json.RawMessage{}
	}
	inputRaw, marshalErr := json.Marshal(inputForMarshal)
	if marshalErr != nil {
		return nil, marshalErr
	}
	return sjson.SetRawBytes(payload, "input", inputRaw)
}

// BuildOpenAIWSCurrentTurnRetryPayload 构造替换账号可用的无链路当前回合请求。
// 只有 input 能完整覆盖所有 function_call_output 时才允许剥离 previous_response_id。
func BuildOpenAIWSCurrentTurnRetryPayload(
	payload []byte,
	fullInput []json.RawMessage,
	fullInputExists bool,
	originalModel string,
) ([]byte, bool, error) {
	if !fullInputExists {
		return nil, false, nil
	}
	retryPayload, err := SetOpenAIWSPayloadInputSequence(payload, fullInput, true)
	if err != nil {
		return nil, false, err
	}
	retryPayload = wire.RemovePreviousResponseIDFromBody(retryPayload)
	if model := strings.TrimSpace(originalModel); model != "" {
		retryPayload, err = sjson.SetBytes(retryPayload, "model", model)
		if err != nil {
			return nil, false, err
		}
	}
	coverage := wire.AnalyzeToolCallOutputContextCoverageBytes(retryPayload)
	if coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs {
		return nil, false, nil
	}
	return retryPayload, true, nil
}

func ShouldKeepIngressPreviousResponseID(
	previousPayload []byte,
	currentPayload []byte,
	lastTurnResponseID string,
	hasFunctionCallOutput bool,
) (bool, string, error) {
	if hasFunctionCallOutput {
		return true, "has_function_call_output", nil
	}
	currentPreviousResponseID := strings.TrimSpace(WSPayloadStringFromRaw(currentPayload, "previous_response_id"))
	if currentPreviousResponseID == "" {
		return false, "missing_previous_response_id", nil
	}
	expectedPreviousResponseID := strings.TrimSpace(lastTurnResponseID)
	if expectedPreviousResponseID == "" {
		return false, "missing_last_turn_response_id", nil
	}
	if currentPreviousResponseID != expectedPreviousResponseID {
		return false, "previous_response_id_mismatch", nil
	}
	if len(previousPayload) == 0 {
		return false, "missing_previous_turn_payload", nil
	}

	previousComparable, previousComparableErr := NormalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(previousPayload)
	if previousComparableErr != nil {
		return false, "non_input_compare_error", previousComparableErr
	}
	currentComparable, currentComparableErr := NormalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(currentPayload)
	if currentComparableErr != nil {
		return false, "non_input_compare_error", currentComparableErr
	}
	if !bytes.Equal(previousComparable, currentComparable) {
		return false, "non_input_changed", nil
	}
	return true, "strict_incremental_ok", nil
}

func BuildOpenAIWSIngressPreviousTurnStrictState(payload []byte) (*WSPreviousTurnStrictState, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	nonInputComparable, nonInputErr := NormalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(payload)
	if nonInputErr != nil {
		return nil, nonInputErr
	}
	return &WSPreviousTurnStrictState{
		nonInputComparable: nonInputComparable,
	}, nil
}

func ShouldKeepIngressPreviousResponseIDWithStrictState(
	previousState *WSPreviousTurnStrictState,
	currentPayload []byte,
	lastTurnResponseID string,
	hasFunctionCallOutput bool,
) (bool, string, error) {
	if hasFunctionCallOutput {
		return true, "has_function_call_output", nil
	}
	currentPreviousResponseID := strings.TrimSpace(WSPayloadStringFromRaw(currentPayload, "previous_response_id"))
	if currentPreviousResponseID == "" {
		return false, "missing_previous_response_id", nil
	}
	expectedPreviousResponseID := strings.TrimSpace(lastTurnResponseID)
	if expectedPreviousResponseID == "" {
		return false, "missing_last_turn_response_id", nil
	}
	if currentPreviousResponseID != expectedPreviousResponseID {
		return false, "previous_response_id_mismatch", nil
	}
	if previousState == nil {
		return false, "missing_previous_turn_payload", nil
	}

	currentComparable, currentComparableErr := NormalizeOpenAIWSPayloadWithoutInputAndPreviousResponseID(currentPayload)
	if currentComparableErr != nil {
		return false, "non_input_compare_error", currentComparableErr
	}
	if !bytes.Equal(previousState.nonInputComparable, currentComparable) {
		return false, "non_input_changed", nil
	}
	return true, "strict_incremental_ok", nil
}

type WSPreviousTurnStrictState struct {
	nonInputComparable []byte
}

const WSMaxPrevResponseIDDeletePasses = 8

func WSPayloadStringFromRaw(payload []byte, key string) string {
	if len(payload) == 0 || strings.TrimSpace(key) == "" {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(payload, key).String())
}
