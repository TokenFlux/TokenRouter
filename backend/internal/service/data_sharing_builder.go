package service

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type dataShareBuildSessionOptions struct {
	FinalizeQuality bool
}

type dataShareCaptureFacts struct {
	now          time.Time
	provider     string
	model        string
	requestPath  string
	userAgent    string
	sessionID    string
	trajectoryID string
	usage        map[string]any
	meta         map[string]any
	inputTokens  int64
	outputTokens int64
	actualCost   *float64
	userID       int64
	apiKeyID     int64
	groupID      int64
}

func newDataShareCaptureFacts(input DataShareCaptureInput) dataShareCaptureFacts {
	now := time.Now()
	groupID := int64(0)
	if input.APIKey != nil && input.APIKey.GroupID != nil {
		groupID = *input.APIKey.GroupID
	} else if input.APIKey != nil && input.APIKey.Group != nil {
		groupID = input.APIKey.Group.ID
	}
	userID := int64(0)
	if input.User != nil {
		userID = input.User.ID
	} else if input.APIKey != nil {
		userID = input.APIKey.UserID
	}
	apiKeyID := int64(0)
	if input.APIKey != nil {
		apiKeyID = input.APIKey.ID
	}
	provider := normalizeDataShareProvider(input.Provider, input.APIKey)
	model := resolveDataShareActualModel(input)
	requestPath := normalizeDataShareRequestPath(input.InboundEndpoint)
	userAgent := normalizeDataShareUserAgent(input.UserAgent)
	sessionID := normalizeDataShareSessionID(input.SessionID, input.RequestID, input.RequestBody, apiKeyID)
	inputTokens := int64(input.InputTokens + input.CacheReadTokens + input.CacheCreateTokens)
	outputTokens := int64(input.OutputTokens)
	return dataShareCaptureFacts{
		now:          now,
		provider:     provider,
		model:        model,
		requestPath:  requestPath,
		userAgent:    userAgent,
		sessionID:    sessionID,
		trajectoryID: buildTrajectoryID(provider, sessionID, apiKeyID, groupID),
		usage:        buildCaptureUsage(input),
		meta:         buildCaptureMeta(input),
		inputTokens:  inputTokens,
		outputTokens: outputTokens,
		actualCost:   cloneFloat64Ptr(input.ActualCost),
		userID:       userID,
		apiKeyID:     apiKeyID,
		groupID:      groupID,
	}
}

func (s *DataSharingService) buildSession(input DataShareCaptureInput) *DataShareSession {
	return s.buildSessionWithOptions(input, dataShareBuildSessionOptions{FinalizeQuality: true})
}

func (s *DataSharingService) buildOpenAIResponsesRawCaptureSession(input DataShareCaptureInput) *DataShareSession {
	facts := newDataShareCaptureFacts(input)
	if input.Turn > 0 {
		facts.meta["turn"] = input.Turn
	}
	if input.CaptureIncomplete {
		facts.meta["capture_incomplete"] = true
	}
	systemPrompt := strings.TrimSpace(input.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromRequest(input.RequestBody)
	}
	inputCopy := cloneDataShareCaptureInput(input)
	requestItems := normalizeOpenAIResponsesRequestInputItems(input.RequestBody)
	responseItems := appendAssistantMessageFromResponse(nil, input.ResponseBody)
	return &DataShareSession{
		TrajectoryID:         facts.trajectoryID,
		SessionID:            facts.sessionID,
		Dataset:              defaultDataShareDataset,
		Provider:             facts.provider,
		Model:                facts.model,
		RequestPath:          facts.requestPath,
		UserAgent:            facts.userAgent,
		Status:               DataShareStatusTerminated,
		IsFinalSnapshot:      false,
		SourceRequestCount:   1,
		SystemPrompt:         optionalDataShareString(systemPrompt),
		Tools:                normalizeCaptureTools(input),
		Usage:                facts.usage,
		Meta:                 facts.meta,
		QualityStatus:        DataShareQualityInvalid,
		Exportable:           false,
		InputTokens:          facts.inputTokens,
		OutputTokens:         facts.outputTokens,
		TotalTokens:          facts.inputTokens + facts.outputTokens,
		ActualCost:           facts.actualCost,
		UserID:               facts.userID,
		APIKeyID:             facts.apiKeyID,
		GroupID:              facts.groupID,
		CreatedAt:            facts.now,
		EndedAt:              &facts.now,
		UpdatedAt:            facts.now,
		captureMode:          dataShareCaptureModeOpenAIResponsesRaw,
		captureInput:         &inputCopy,
		captureRequestItems:  cloneDataShareResponsesInputItems(requestItems),
		captureResponseItems: cloneBufferedDataShareMaps(responseItems),
	}
}

func dataShareCaptureInputIsOpenAIResponses(input DataShareCaptureInput) bool {
	if input.CaptureMode == dataShareCaptureModeOpenAIResponsesRaw {
		return true
	}
	if normalizeDataShareRequestPath(input.InboundEndpoint) != "/v1/responses" {
		return false
	}
	if len(input.RequestBody) == 0 {
		return false
	}
	return gjson.GetBytes(input.RequestBody, "input").Exists()
}

func (s *DataSharingService) buildMessagesRawCaptureSession(input DataShareCaptureInput) *DataShareSession {
	facts := newDataShareCaptureFacts(input)
	if input.CaptureIncomplete {
		facts.meta["capture_incomplete"] = true
	}
	systemPrompt := strings.TrimSpace(input.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromRequest(input.RequestBody)
	}
	inputCopy := cloneDataShareCaptureInput(input)
	requestMessages := normalizeCaptureRequestMessages(input)
	responseMessages := normalizeCaptureResponseMessages(input)
	messages := append(cloneBufferedDataShareMaps(requestMessages), cloneBufferedDataShareMaps(responseMessages)...)
	return &DataShareSession{
		TrajectoryID:            facts.trajectoryID,
		SessionID:               facts.sessionID,
		Dataset:                 defaultDataShareDataset,
		Provider:                facts.provider,
		Model:                   facts.model,
		RequestPath:             facts.requestPath,
		UserAgent:               facts.userAgent,
		Status:                  DataShareStatusTerminated,
		IsFinalSnapshot:         false,
		SourceRequestCount:      1,
		SystemPrompt:            optionalDataShareString(systemPrompt),
		Tools:                   normalizeCaptureTools(input),
		Messages:                messages,
		Usage:                   facts.usage,
		Meta:                    facts.meta,
		QualityStatus:           DataShareQualityInvalid,
		Exportable:              false,
		InputTokens:             facts.inputTokens,
		OutputTokens:            facts.outputTokens,
		TotalTokens:             facts.inputTokens + facts.outputTokens,
		ActualCost:              facts.actualCost,
		UserID:                  facts.userID,
		APIKeyID:                facts.apiKeyID,
		GroupID:                 facts.groupID,
		CreatedAt:               facts.now,
		EndedAt:                 &facts.now,
		UpdatedAt:               facts.now,
		captureMode:             dataShareCaptureModeMessagesRaw,
		captureInput:            &inputCopy,
		captureRequestMessages:  cloneBufferedDataShareMaps(requestMessages),
		captureResponseMessages: cloneBufferedDataShareMaps(responseMessages),
	}
}

func dataShareCaptureInputIsMessagesRaw(input DataShareCaptureInput) bool {
	if input.CaptureMode == dataShareCaptureModeMessagesRaw {
		return true
	}
	if normalizeDataShareRequestPath(input.InboundEndpoint) != "/v1/messages" {
		return false
	}
	if len(input.Messages) > 0 {
		return true
	}
	return len(input.RequestBody) > 0 && gjson.GetBytes(input.RequestBody, "messages").IsArray()
}

func optionalDataShareString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	out := value
	return &out
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneDataShareCaptureInput(input DataShareCaptureInput) DataShareCaptureInput {
	clone := input
	clone.RequestBody = cloneDataSharingRequestBody(input.RequestBody)
	clone.ResponseBody = cloneDataSharingRequestBody(input.ResponseBody)
	if input.ActualCost != nil {
		actualCost := *input.ActualCost
		clone.ActualCost = &actualCost
	}
	if len(input.Messages) > 0 {
		clone.Messages = append([]any(nil), input.Messages...)
	}
	clone.Tools = cloneBufferedDataShareMaps(input.Tools)
	return clone
}

func (s *DataSharingService) buildSessionWithOptions(input DataShareCaptureInput, opts dataShareBuildSessionOptions) *DataShareSession {
	facts := newDataShareCaptureFacts(input)
	messages := normalizeCaptureMessages(input)
	tools := normalizeCaptureTools(input)
	systemPrompt := strings.TrimSpace(input.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromRequest(input.RequestBody)
	}
	if systemPrompt == "" {
		systemPrompt = extractSystemPromptFromMessages(messages)
	}
	qualityStatus := DataShareQualityInvalid
	qualityErrors := []string(nil)
	status := DataShareStatusTerminated
	finalSnapshot := false
	if opts.FinalizeQuality {
		qualityReport := evaluateDataShareSessionQuality(facts.model, systemPrompt, messages, tools, facts.usage)
		qualityErrors = qualityReport.Errors
		qualityStatus = qualityReport.Status
		status, finalSnapshot = dataShareCompletionState(qualityStatus)
	}
	sessionJSON := map[string]any{
		"trajectory_id":        facts.trajectoryID,
		"session_id":           facts.sessionID,
		"dataset":              defaultDataShareDataset,
		"provider":             facts.provider,
		"model":                facts.model,
		"request_path":         facts.requestPath,
		"user_agent":           facts.userAgent,
		"created_at":           facts.now.Format(time.RFC3339Nano),
		"ended_at":             facts.now.Format(time.RFC3339Nano),
		"status":               status,
		"is_final_snapshot":    finalSnapshot,
		"source_request_count": 1,
		"quality_status":       qualityStatus,
		"system_prompt":        systemPrompt,
		"tools":                tools,
		"messages":             messages,
		"usage":                facts.usage,
		"meta":                 facts.meta,
	}
	storageBytes := int64(0)
	if opts.FinalizeQuality {
		storageBytes = int64(len(mustJSON(sessionJSON)))
	} else {
		sessionJSON = nil
	}
	var sysPtr *string
	if systemPrompt != "" {
		sysPtr = &systemPrompt
	}
	return &DataShareSession{
		TrajectoryID:       facts.trajectoryID,
		SessionID:          facts.sessionID,
		Dataset:            defaultDataShareDataset,
		Provider:           facts.provider,
		Model:              facts.model,
		RequestPath:        facts.requestPath,
		UserAgent:          facts.userAgent,
		Status:             status,
		IsFinalSnapshot:    finalSnapshot,
		SourceRequestCount: 1,
		SystemPrompt:       sysPtr,
		Tools:              tools,
		Messages:           messages,
		Usage:              facts.usage,
		Meta:               facts.meta,
		SessionJSON:        sessionJSON,
		Exportable:         DataShareQualityExportable(qualityStatus),
		QualityStatus:      qualityStatus,
		QualityErrors:      qualityErrors,
		StorageBytes:       storageBytes,
		InputTokens:        facts.inputTokens,
		OutputTokens:       facts.outputTokens,
		TotalTokens:        facts.inputTokens + facts.outputTokens,
		ActualCost:         facts.actualCost,
		UserID:             facts.userID,
		APIKeyID:           facts.apiKeyID,
		GroupID:            facts.groupID,
		CreatedAt:          facts.now,
		EndedAt:            &facts.now,
		UpdatedAt:          facts.now,
	}
}

func normalizeCaptureMessages(input DataShareCaptureInput) []map[string]any {
	out := normalizeCaptureRequestMessages(input)
	out = append(out, normalizeCaptureResponseMessages(input)...)
	return normalizeDataShareMessages(out)
}

func normalizeCaptureRequestMessages(input DataShareCaptureInput) []map[string]any {
	var out []map[string]any
	if len(input.Messages) > 0 {
		out = appendAnyMessages(out, input.Messages)
	}
	if len(out) == 0 && len(input.RequestBody) > 0 {
		out = appendRequestMessages(out, input.RequestBody)
	}
	return normalizeDataShareMessages(out)
}

func normalizeCaptureResponseMessages(input DataShareCaptureInput) []map[string]any {
	var out []map[string]any
	if len(input.ResponseBody) > 0 {
		out = appendAssistantMessageFromResponse(out, input.ResponseBody)
	}
	return normalizeDataShareMessages(out)
}

func appendAnyMessages(out []map[string]any, messages []any) []map[string]any {
	for _, msg := range messages {
		switch v := msg.(type) {
		case map[string]any:
			out = append(out, v)
		default:
			out = append(out, map[string]any{"role": "unknown", "content": v})
		}
	}
	return out
}

func appendRequestMessages(out []map[string]any, body []byte) []map[string]any {
	startLen := len(out)
	if arr := gjson.GetBytes(body, "messages"); arr.IsArray() {
		for _, item := range arr.Array() {
			out = append(out, rawJSONToMap(item.Raw))
		}
	}
	if arr := gjson.GetBytes(body, "contents"); arr.IsArray() {
		for _, item := range arr.Array() {
			msg := rawJSONToMap(item.Raw)
			if role, ok := msg["role"].(string); ok && role == "model" {
				msg["role"] = "assistant"
			}
			out = append(out, msg)
		}
	}
	if len(out) == startLen {
		// OpenAI Responses 使用 input 承载对话上下文，Codex CLI 会走这条协议。
		out = appendResponsesInputMessages(out, gjson.GetBytes(body, "input"))
	}
	return out
}

func appendResponsesInputMessages(out []map[string]any, input gjson.Result) []map[string]any {
	if !input.Exists() {
		return out
	}
	if input.Type == gjson.String {
		return append(out, map[string]any{"role": "user", "content": input.String()})
	}
	if input.IsObject() {
		return append(out, normalizeResponsesInputItem(input))
	}
	if !input.IsArray() {
		return out
	}
	for _, item := range input.Array() {
		if item.Type == gjson.String {
			out = append(out, map[string]any{"role": "user", "content": item.String()})
			continue
		}
		if item.IsObject() {
			out = append(out, normalizeResponsesInputItem(item))
		}
	}
	return out
}

func normalizeResponsesInputItem(item gjson.Result) map[string]any {
	msg := rawJSONToMap(item.Raw)
	role := normalizeResponsesInputRole(item.Get("role").String(), item.Get("type").String())
	if role != "" {
		msg["role"] = role
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	switch itemType {
	case "function_call":
		// 工具调用在对话中等价于 assistant 发起的 tool_call。
		return normalizeResponsesFunctionCallMessage(msg)
	case "function_call_output":
		// 工具执行结果按 tool 消息保存，便于后续训练流水线识别。
		return normalizeToolResultMessage(msg)
	case "input_text", "text":
		msg["role"] = "user"
		if text := item.Get("text"); text.Exists() {
			msg["content"] = text.String()
		}
	}
	if _, ok := msg["content"]; !ok {
		if content := item.Get("content"); content.Exists() {
			msg["content"] = responseInputContentValue(content)
		} else if text := item.Get("text"); text.Exists() {
			msg["content"] = text.String()
		}
	}
	return msg
}

func normalizeResponsesInputRole(role string, itemType string) string {
	role = strings.TrimSpace(role)
	switch role {
	case "developer":
		return "system"
	case "model":
		return "assistant"
	case "":
		switch strings.TrimSpace(itemType) {
		case "function_call":
			return "assistant"
		case "function_call_output":
			return "tool"
		default:
			return "user"
		}
	default:
		return role
	}
}

func responseInputContentValue(value gjson.Result) any {
	if value.Type == gjson.String {
		return value.String()
	}
	return normalizeDataShareContentValue(rawJSONToAny(value.Raw))
}

func appendAssistantMessageFromResponse(out []map[string]any, body []byte) []map[string]any {
	if msg := gjson.GetBytes(body, "choices.0.message"); msg.Exists() {
		out = append(out, rawJSONToMap(msg.Raw))
	}
	if output := gjson.GetBytes(body, "output"); output.IsArray() {
		for _, item := range output.Array() {
			if item.IsObject() {
				out = append(out, normalizeResponsesOutputItem(item))
			}
		}
	}
	if content := gjson.GetBytes(body, "content"); content.IsArray() {
		out = append(out, map[string]any{"role": "assistant", "content": responseInputContentValue(content)})
	}
	if candidates := gjson.GetBytes(body, "candidates.0.content"); candidates.Exists() {
		msg := rawJSONToMap(candidates.Raw)
		msg["role"] = "assistant"
		out = append(out, msg)
	}
	return out
}

type dataShareResponsesCaptureState struct {
	StableIDs        map[string]struct{} `json:"stable_ids,omitempty"`
	ReplayIdentities []string            `json:"replay_identities,omitempty"`
	ResponseKeys     map[string]struct{} `json:"response_keys,omitempty"`
	LastTurn         int                 `json:"last_turn,omitempty"`
	OrderUncertain   bool                `json:"order_uncertain,omitempty"`
}

func (s *DataSharingService) buildOpenAIResponsesIncrementalSession(existing *DataShareSession, raw *DataShareSession) *DataShareSession {
	if raw == nil || raw.captureInput == nil {
		return raw
	}
	out := cloneBufferedDataShareSession(raw)
	out.captureMode = dataShareCaptureModeIncremental
	input := *raw.captureInput
	state := cloneDataShareResponsesCaptureState(captureStateFromDataShareSession(existing))
	if state == nil {
		state = &dataShareResponsesCaptureState{}
	}
	if input.Turn > 0 && state.LastTurn > 0 && input.Turn <= state.LastTurn {
		state.OrderUncertain = true
	}
	requestItems := cloneDataShareResponsesInputItems(raw.captureRequestItems)
	if len(requestItems) == 0 {
		requestItems = normalizeOpenAIResponsesRequestInputItems(input.RequestBody)
	}
	replayPlan, orderUncertain := dataShareBuildResponsesReplayPlan(state, requestItems)
	if orderUncertain {
		state.OrderUncertain = true
	}
	messages := make([]map[string]any, 0, dataShareResponsesReplayPlanKeepCount(replayPlan)+2)
	for index, item := range requestItems {
		if !replayPlan.Keep[index] {
			continue
		}
		messages = append(messages, cloneDataShareMap(item.Message))
	}
	responseStart := len(messages)
	responseItems := cloneBufferedDataShareMaps(raw.captureResponseItems)
	if len(responseItems) == 0 {
		responseItems = appendAssistantMessageFromResponse(nil, input.ResponseBody)
	}
	messages = append(messages, responseItems...)
	messages = dataShareResponsesFilterKnownResponseMessages(state, requestItems, messages, responseStart)
	if input.CaptureIncomplete && len(messages) == responseStart {
		out.Meta["capture_incomplete"] = true
	}
	out.Messages = normalizeDataShareMessages(messages)
	out.captureState = updateDataShareResponsesCaptureState(state, requestItems, replayPlan.Keep, messages[responseStart:], input.Turn)
	out.Meta = withDataShareInternalCaptureState(out.Meta, out.captureState)
	return out
}

type dataShareResponsesInputItem struct {
	Message     map[string]any
	StableID    string
	Identity    string
	IdentityKey string
}

type dataShareResponsesReplayPlan struct {
	Keep []bool
}

func normalizeOpenAIResponsesRequestInputItems(body []byte) []dataShareResponsesInputItem {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return nil
	}
	var out []dataShareResponsesInputItem
	appendItem := func(item gjson.Result) {
		msg := map[string]any(nil)
		if item.Type == gjson.String {
			msg = map[string]any{"role": "user", "content": item.String()}
		} else if item.IsObject() {
			msg = normalizeResponsesInputItem(item)
		}
		if len(msg) == 0 {
			return
		}
		msg = normalizeDataShareMessage(msg)
		identity := dataShareMessageIdentity(msg)
		out = append(out, dataShareResponsesInputItem{
			Message:     msg,
			StableID:    dataShareResponsesStableItemID(item, msg),
			Identity:    identity,
			IdentityKey: dataShareResponsesIdentityKey(identity),
		})
	}
	if input.Type == gjson.String || input.IsObject() {
		appendItem(input)
		return out
	}
	if !input.IsArray() {
		return nil
	}
	for _, item := range input.Array() {
		appendItem(item)
	}
	return out
}

func dataShareResponsesStableItemID(item gjson.Result, msg map[string]any) string {
	itemType := strings.TrimSpace(item.Get("type").String())
	for _, candidate := range []string{
		item.Get("call_id").String(),
		item.Get("tool_call_id").String(),
		item.Get("tool_use_id").String(),
		stringFromAny(msg["tool_call_id"]),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return itemType + ":" + candidate
		}
	}
	if itemType != "message" {
		if id := strings.TrimSpace(item.Get("id").String()); id != "" {
			return itemType + ":" + id
		}
	}
	if calls := anySlice(msg["tool_calls"]); len(calls) > 0 {
		call, _ := mapFromAny(calls[0])
		id := firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"]))
		if id != "" {
			return "function_call:" + id
		}
	}
	return ""
}

func dataShareBuildResponsesReplayPlan(state *dataShareResponsesCaptureState, incoming []dataShareResponsesInputItem) (dataShareResponsesReplayPlan, bool) {
	plan := dataShareResponsesReplayPlan{Keep: make([]bool, len(incoming))}
	for i := range plan.Keep {
		plan.Keep[i] = true
	}
	if state == nil || len(incoming) == 0 {
		return plan, false
	}
	prefixReplay := 0
	for prefixReplay < len(incoming) {
		item := incoming[prefixReplay]
		if item.StableID != "" {
			if _, ok := state.StableIDs[item.StableID]; ok {
				prefixReplay++
				continue
			}
			break
		}
		if prefixReplay < len(state.ReplayIdentities) && state.ReplayIdentities[prefixReplay] == item.IdentityKey {
			prefixReplay++
			continue
		}
		break
	}
	// 没有稳定 id 的部分前缀命中可能是分叉；保留这段前缀，但仍继续扫描后续明确的长窗口 replay。
	prefixOrderUncertain := prefixReplay > 0 && prefixReplay < len(incoming) && !dataShareResponsesPrefixHasStableAnchor(incoming[:prefixReplay])
	if !prefixOrderUncertain {
		for i := 0; i < prefixReplay; i++ {
			plan.Keep[i] = false
		}
	}
	if state == nil || len(state.ReplayIdentities) < dataShareLongReplayMinMessages || len(incoming) < dataShareLongReplayMinMessages {
		return plan, prefixOrderUncertain
	}
	incomingKeys := dataShareResponsesInputIdentityKeys(incoming)
	index := dataShareReplayWindowIndex(state.ReplayIdentities)
	for i := prefixReplay; i < len(incoming); {
		match := dataShareBestIndexedReplayMatch(state.ReplayIdentities, index, incomingKeys, i)
		if match.length < dataShareLongReplayMinMessages {
			i++
			continue
		}
		for end := i + match.length; i < end; i++ {
			plan.Keep[i] = false
		}
	}
	return plan, prefixOrderUncertain
}

func dataShareResponsesReplayPlanKeepCount(plan dataShareResponsesReplayPlan) int {
	count := 0
	for _, keep := range plan.Keep {
		if keep {
			count++
		}
	}
	return count
}

func dataShareResponsesInputIdentityKeys(items []dataShareResponsesInputItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.IdentityKey
	}
	return out
}

func dataShareResponsesPrefixHasStableAnchor(items []dataShareResponsesInputItem) bool {
	for _, item := range items {
		if item.StableID != "" {
			return true
		}
	}
	return false
}

func dataShareResponsesFilterKnownResponseMessages(state *dataShareResponsesCaptureState, requestItems []dataShareResponsesInputItem, messages []map[string]any, responseStart int) []map[string]any {
	if state == nil || len(state.ResponseKeys) == 0 || responseStart >= len(messages) {
		return messages
	}
	out := cloneBufferedDataShareMaps(messages[:responseStart])
	context := dataShareResponsesContextSeed()
	for _, item := range requestItems {
		context = dataShareResponsesAdvanceContext(context, item.IdentityKey)
	}
	for _, msg := range messages[responseStart:] {
		identity := dataShareMessageIdentity(msg)
		identityKey := dataShareResponsesIdentityKey(identity)
		seen := false
		if identity != "" {
			key := dataShareResponsesScopedResponseKey(context, identityKey)
			if _, ok := state.ResponseKeys[key]; ok {
				seen = true
			}
		}
		context = dataShareResponsesAdvanceContext(context, identityKey)
		if seen {
			continue
		}
		out = append(out, cloneDataShareMap(msg))
	}
	return out
}

func updateDataShareResponsesCaptureState(state *dataShareResponsesCaptureState, requestItems []dataShareResponsesInputItem, keepRequestItems []bool, responseMessages []map[string]any, turn int) *dataShareResponsesCaptureState {
	if state == nil {
		state = &dataShareResponsesCaptureState{}
	}
	if state.StableIDs == nil {
		state.StableIDs = map[string]struct{}{}
	}
	if state.ResponseKeys == nil {
		state.ResponseKeys = map[string]struct{}{}
	}
	if len(keepRequestItems) != len(requestItems) {
		keepRequestItems = make([]bool, len(requestItems))
		for i := range keepRequestItems {
			keepRequestItems[i] = true
		}
	}
	context := dataShareResponsesContextSeed()
	for index, item := range requestItems {
		if dataShareResponsesInputItemLooksAssistantOutput(item) {
			// 只记录“前文 + assistant 输出”的固定长度作用域 key，避免误删合法重复回答，也避免 meta 随文本长度膨胀。
			state.ResponseKeys[dataShareResponsesScopedResponseKey(context, item.IdentityKey)] = struct{}{}
		}
		if keepRequestItems[index] {
			if item.StableID != "" {
				state.StableIDs[item.StableID] = struct{}{}
			}
			state.ReplayIdentities = append(state.ReplayIdentities, item.IdentityKey)
		}
		context = dataShareResponsesAdvanceContext(context, item.IdentityKey)
	}
	for _, msg := range responseMessages {
		for _, stableID := range dataShareResponsesStableIDsFromMessage(msg) {
			state.StableIDs[stableID] = struct{}{}
		}
		identity := dataShareMessageIdentity(msg)
		identityKey := dataShareResponsesIdentityKey(identity)
		if identity != "" {
			state.ResponseKeys[dataShareResponsesScopedResponseKey(context, identityKey)] = struct{}{}
		}
		state.ReplayIdentities = append(state.ReplayIdentities, identityKey)
		context = dataShareResponsesAdvanceContext(context, identityKey)
	}
	if turn > state.LastTurn {
		state.LastTurn = turn
	}
	return state
}

func dataShareResponsesIdentityKey(identity string) string {
	if identity == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

func cloneDataShareResponsesInputItems(items []dataShareResponsesInputItem) []dataShareResponsesInputItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]dataShareResponsesInputItem, 0, len(items))
	for _, item := range items {
		out = append(out, dataShareResponsesInputItem{
			Message:     cloneDataShareMap(item.Message),
			StableID:    item.StableID,
			Identity:    item.Identity,
			IdentityKey: item.IdentityKey,
		})
	}
	return out
}

func dataShareResponsesContextSeed() string {
	return strings.Repeat("0", 32)
}

func dataShareResponsesAdvanceContext(context string, identityKey string) string {
	sum := sha256.Sum256([]byte(context + "\x00" + identityKey))
	return hex.EncodeToString(sum[:16])
}

func dataShareResponsesScopedResponseKey(context string, responseIdentityKey string) string {
	if responseIdentityKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(context + "\x01" + responseIdentityKey))
	return hex.EncodeToString(sum[:16])
}

func dataShareResponsesInputItemLooksAssistantOutput(item dataShareResponsesInputItem) bool {
	role := strings.TrimSpace(stringFromAny(item.Message["role"]))
	return role == "assistant"
}

func dataShareResponsesStableIDsFromMessage(msg map[string]any) []string {
	if len(msg) == 0 {
		return nil
	}
	var out []string
	role := strings.TrimSpace(stringFromAny(msg["role"]))
	if role == "assistant" {
		for _, raw := range anySlice(msg["tool_calls"]) {
			call, ok := mapFromAny(raw)
			if !ok {
				continue
			}
			id := firstNonBlank(stringFromAny(call["id"]), stringFromAny(call["call_id"]), stringFromAny(call["tool_call_id"]))
			if id != "" {
				// Responses 下一轮 input 会把上一轮 output.function_call 用 call_id 回放，必须写入 replay 锚点。
				out = append(out, "function_call:"+id)
			}
		}
	}
	if role == "tool" {
		id := firstNonBlank(stringFromAny(msg["tool_call_id"]), stringFromAny(msg["call_id"]), stringFromAny(msg["tool_use_id"]))
		if id != "" {
			out = append(out, "function_call_output:"+id)
		}
	}
	return out
}

func captureStateFromDataShareSession(session *DataShareSession) *dataShareResponsesCaptureState {
	if session == nil {
		return nil
	}
	if session.captureState != nil {
		return cloneDataShareResponsesCaptureState(session.captureState)
	}
	if meta := mapAnyFromAny(session.Meta[dataShareInternalCaptureMetaKey]); len(meta) > 0 {
		return dataShareResponsesCaptureStateFromMap(meta)
	}
	if meta := mapAnyFromAny(session.SessionJSON["meta"]); len(meta) > 0 {
		if captureMeta := mapAnyFromAny(meta[dataShareInternalCaptureMetaKey]); len(captureMeta) > 0 {
			return dataShareResponsesCaptureStateFromMap(captureMeta)
		}
	}
	return nil
}

func dataShareResponsesCaptureStateFromMap(meta map[string]any) *dataShareResponsesCaptureState {
	if len(meta) == 0 {
		return nil
	}
	state := &dataShareResponsesCaptureState{
		StableIDs:        map[string]struct{}{},
		ReplayIdentities: stringsFromAny(meta["replay_identities"]),
		ResponseKeys:     map[string]struct{}{},
		LastTurn:         intFromAny(meta["last_turn"]),
		OrderUncertain:   boolFromAny(meta["order_uncertain"]),
	}
	for _, id := range stringsFromAny(meta["stable_ids"]) {
		if id != "" {
			state.StableIDs[id] = struct{}{}
		}
	}
	for _, key := range stringsFromAny(meta["response_keys"]) {
		if key != "" {
			state.ResponseKeys[key] = struct{}{}
		}
	}
	return state
}

func cloneDataShareResponsesCaptureState(state *dataShareResponsesCaptureState) *dataShareResponsesCaptureState {
	if state == nil {
		return nil
	}
	out := &dataShareResponsesCaptureState{
		StableIDs:        map[string]struct{}{},
		ReplayIdentities: append([]string(nil), state.ReplayIdentities...),
		ResponseKeys:     map[string]struct{}{},
		LastTurn:         state.LastTurn,
		OrderUncertain:   state.OrderUncertain,
	}
	for id := range state.StableIDs {
		out.StableIDs[id] = struct{}{}
	}
	for key := range state.ResponseKeys {
		out.ResponseKeys[key] = struct{}{}
	}
	return out
}

func withDataShareInternalCaptureState(meta map[string]any, state *dataShareResponsesCaptureState) map[string]any {
	out := cloneDataShareMap(meta)
	if state == nil {
		delete(out, dataShareInternalCaptureMetaKey)
		return out
	}
	stableIDs := make([]string, 0, len(state.StableIDs))
	for id := range state.StableIDs {
		stableIDs = append(stableIDs, id)
	}
	sort.Strings(stableIDs)
	responseKeys := make([]string, 0, len(state.ResponseKeys))
	for key := range state.ResponseKeys {
		responseKeys = append(responseKeys, key)
	}
	sort.Strings(responseKeys)
	out[dataShareInternalCaptureMetaKey] = map[string]any{
		"stable_ids":        stableIDs,
		"replay_identities": append([]string(nil), state.ReplayIdentities...),
		"response_keys":     responseKeys,
		"last_turn":         state.LastTurn,
		"order_uncertain":   state.OrderUncertain,
		"schema":            "openai_responses_v1",
	}
	if state.OrderUncertain {
		out["capture_order_uncertain"] = true
	}
	return out
}

func stripDataShareInternalCaptureStateFromMeta(meta map[string]any) map[string]any {
	out := cloneDataShareMap(meta)
	delete(out, dataShareInternalCaptureMetaKey)
	return out
}

func normalizeResponsesOutputItem(item gjson.Result) map[string]any {
	msg := rawJSONToMap(item.Raw)
	switch strings.TrimSpace(item.Get("type").String()) {
	case "function_call":
		// Responses API 的 output 也可能直接携带工具调用，需要转成统一 tool_calls。
		return normalizeResponsesFunctionCallMessage(msg)
	case "function_call_output":
		return normalizeToolResultMessage(msg)
	case "message":
		role := normalizeResponsesInputRole(item.Get("role").String(), item.Get("type").String())
		if strings.TrimSpace(item.Get("role").String()) == "" {
			role = "assistant"
		}
		out := map[string]any{"role": role}
		if content := item.Get("content"); content.Exists() {
			out["content"] = responseInputContentValue(content)
		}
		if phase := strings.TrimSpace(item.Get("phase").String()); phase != "" {
			// Codex Responses 会用 phase 标记 commentary 等可见中间输出，保留后才能和下一轮 input 回放稳定对齐。
			out["phase"] = phase
		}
		return out
	default:
		return normalizeDataShareMessage(msg)
	}
}

func normalizeCaptureTools(input DataShareCaptureInput) []map[string]any {
	if len(input.Tools) > 0 {
		return normalizeDataShareTools(input.Tools)
	}
	body := input.RequestBody
	var out []map[string]any
	for _, path := range []string{"tools", "functions"} {
		if arr := gjson.GetBytes(body, path); arr.IsArray() {
			for _, item := range arr.Array() {
				out = append(out, rawJSONToMap(item.Raw))
			}
		}
	}
	return normalizeDataShareTools(out)
}

func buildCaptureUsage(input DataShareCaptureInput) map[string]any {
	totalInput := input.InputTokens + input.CacheReadTokens + input.CacheCreateTokens
	total := totalInput + input.OutputTokens
	return map[string]any{
		"input_tokens":                input.InputTokens,
		"output_tokens":               input.OutputTokens,
		"cache_read_input_tokens":     input.CacheReadTokens,
		"cache_creation_input_tokens": input.CacheCreateTokens,
		"total_tokens":                total,
	}
}

func buildCaptureMeta(input DataShareCaptureInput) map[string]any {
	requestID := resolveDataShareRequestID(input)
	requestPath := normalizeDataShareRequestPath(input.InboundEndpoint)
	sourceRequestIDs := []string{}
	if requestID != "" {
		sourceRequestIDs = append(sourceRequestIDs, requestID)
	}
	meta := map[string]any{
		"api_key_id":         int64(0),
		"group_id":           int64(0),
		"account_id":         int64(0),
		"request_id":         requestID,
		"source_request_ids": sourceRequestIDs,
		"requested_model":    firstNonBlank(input.Model, gjson.GetBytes(input.RequestBody, "model").String()),
		"inbound_endpoint":   requestPath,
		"request_path":       requestPath,
		"upstream_endpoint":  input.UpstreamEndpoint,
		"user_agent":         input.UserAgent,
		"user_agent_family":  normalizeDataShareUserAgent(input.UserAgent),
		"ip_address":         input.IPAddress,
	}
	if input.APIKey != nil {
		meta["user_id"] = input.APIKey.UserID
		meta["api_key_id"] = input.APIKey.ID
		meta["api_key_name"] = input.APIKey.Name
		if input.APIKey.GroupID != nil {
			meta["group_id"] = *input.APIKey.GroupID
		}
		if input.APIKey.User != nil {
			meta["user_name"] = input.APIKey.User.Username
			meta["user_email"] = input.APIKey.User.Email
		}
		if input.APIKey.Group != nil {
			meta["group_id"] = input.APIKey.Group.ID
			meta["group_name"] = input.APIKey.Group.Name
		}
	}
	if input.User != nil {
		meta["user_id"] = input.User.ID
		meta["user_name"] = input.User.Username
		meta["user_email"] = input.User.Email
	}
	if input.Account != nil {
		meta["account_id"] = input.Account.ID
	}
	return meta
}

func resolveDataShareRequestID(input DataShareCaptureInput) string {
	return firstNonBlank(
		input.RequestID,
		gjson.GetBytes(input.ResponseBody, "id").String(),
		gjson.GetBytes(input.RequestBody, "request_id").String(),
		gjson.GetBytes(input.RequestBody, "metadata.request_id").String(),
	)
}

func resolveDataShareActualModel(input DataShareCaptureInput) string {
	// 正式交付要求 model 等于实际生成模型；映射后的上游模型优先，客户端请求模型只放入 meta。
	return firstNonBlank(input.UpstreamModel, input.Model, gjson.GetBytes(input.RequestBody, "model").String())
}
