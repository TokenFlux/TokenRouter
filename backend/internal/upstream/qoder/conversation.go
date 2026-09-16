// Qoder 会话增量状态仅由平台拥有；身份和请求元数据显式传入。
package qoder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// RequestMetadata 是解析完成的入站投影，不保存 Gin 或完整账号。
type RequestMetadata struct {
	APIKeyID   int64
	ClaudeCode bool
	Headers    http.Header
}

type QoderConversationStore struct {
	Mu    sync.Mutex
	Ttl   time.Duration
	Items map[string]*QoderConversationState
	// aliases 将外部可见的 response/session id 映射到规范 conversation key。
	// OpenAI Responses previous_response_id 会使用这张表。
	Aliases map[string]string
}

type QoderConversationState struct {
	SessionID           string
	SystemFingerprint   string
	ToolsFingerprint    string
	MessageFingerprints []string
	HasUsage            bool
	LastUsageInput      int
	LastUsageOutput     int
	ExpiresAt           time.Time
	Version             int64 // 版本号，用于检测 rollback 竞态
}

type QoderConversationPlan struct {
	Store                 *QoderConversationStore
	Key                   string
	SessionID             string
	MessagesToSend        []QoderMessage
	IncludeSystem         bool
	IncludeTools          bool
	Reused                bool
	Fallback              bool
	SystemFingerprint     string
	ToolsFingerprint      string
	MessageFingerprints   []string
	CommittedFingerprints []string
	HasPreviousUsage      bool
	PreviousUsageInput    int
	PreviousUsageOutput   int
	MatchStatus           string
	PreviousMessageCount  int
	PrefixMessageCount    int
	StoreItemCount        int
	PreviousFirstHash     string
	CurrentFirstHash      string
	Diagnostics           QoderConversationDiagnostics
	PreviousState         *QoderConversationState
	AcceptedState         *QoderConversationState
	AcceptedCommitted     bool
}

type QoderConversationDiagnostics struct {
	Protocol             string
	Model                string
	KeySource            string
	RequestID            string
	OriginalMessages     int
	SentMessages         int
	OriginalToolsCount   int
	SentToolsCount       int
	OriginalToolsBytes   int
	SentToolsBytes       int
	SystemBytes          int
	SentSystemBytes      int
	OutboundPayloadBytes int
}

func NewQoderConversationStore(ttl time.Duration) *QoderConversationStore {
	if ttl <= 0 {
		ttl = QoderConversationTTL
	}
	return &QoderConversationStore{
		Ttl:     ttl,
		Items:   make(map[string]*QoderConversationState),
		Aliases: make(map[string]string),
	}
}

type QoderConversationPlanOptions struct {
	AppendToExisting bool
}

func (s *QoderConversationStore) Plan(key, system string, tools []any, messages []QoderMessage) *QoderConversationPlan {
	return s.PlanWithOptions(key, system, tools, messages, QoderConversationPlanOptions{})
}

func (s *QoderConversationStore) PlanWithOptions(key, system string, tools []any, messages []QoderMessage, options QoderConversationPlanOptions) *QoderConversationPlan {
	systemFingerprint := QoderSystemFingerprint(system)
	toolsFingerprint := QoderFingerprintAny(tools)
	messageFingerprints := QoderMessageFingerprints(messages)
	currentFirstHash := FirstQoderFingerprint(messageFingerprints)
	fullPlan := func(fallback bool, matchStatus string, state *QoderConversationState, storeItemCount int) *QoderConversationPlan {
		previousMessageCount := 0
		previousFirstHash := ""
		if state != nil {
			previousMessageCount = len(state.MessageFingerprints)
			previousFirstHash = FirstQoderFingerprint(state.MessageFingerprints)
		}
		return &QoderConversationPlan{
			Store:                 s,
			Key:                   key,
			SessionID:             QoderSessionIDForConversation(key, systemFingerprint, toolsFingerprint, messageFingerprints),
			MessagesToSend:        messages,
			IncludeSystem:         true,
			IncludeTools:          true,
			Fallback:              fallback,
			SystemFingerprint:     systemFingerprint,
			ToolsFingerprint:      toolsFingerprint,
			MessageFingerprints:   messageFingerprints,
			CommittedFingerprints: messageFingerprints,
			MatchStatus:           matchStatus,
			PreviousMessageCount:  previousMessageCount,
			StoreItemCount:        storeItemCount,
			PreviousFirstHash:     previousFirstHash,
			CurrentFirstHash:      currentFirstHash,
			PreviousState:         CloneQoderConversationState(state),
		}
	}
	if s == nil || strings.TrimSpace(key) == "" {
		plan := fullPlan(true, "disabled", nil, 0)
		plan.Store = nil
		plan.Key = ""
		plan.SessionID = uuid.NewString()
		return plan
	}

	now := time.Now()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Items == nil {
		s.Items = make(map[string]*QoderConversationState)
	}
	if s.Aliases == nil {
		s.Aliases = make(map[string]string)
	}
	s.PruneExpiredLocked(now)
	key = s.ResolveAliasLocked(key)
	storeItemCount := len(s.Items)
	state := s.Items[key]
	if state == nil {
		return fullPlan(false, "no_state", nil, storeItemCount)
	}
	if now.After(state.ExpiresAt) {
		delete(s.Items, key)
		return fullPlan(true, "expired", state, storeItemCount)
	}
	effectiveSystemFingerprint := systemFingerprint
	if state.SystemFingerprint != systemFingerprint {
		if !options.AppendToExisting || strings.TrimSpace(system) != "" {
			return fullPlan(true, "system_mismatch", state, storeItemCount)
		}
		effectiveSystemFingerprint = state.SystemFingerprint
	}
	effectiveToolsFingerprint := toolsFingerprint
	if state.ToolsFingerprint != toolsFingerprint {
		if !options.AppendToExisting || len(tools) != 0 {
			return fullPlan(true, "tools_mismatch", state, storeItemCount)
		}
		effectiveToolsFingerprint = state.ToolsFingerprint
	}
	if state.SystemFingerprint != effectiveSystemFingerprint {
		return fullPlan(true, "system_mismatch", state, storeItemCount)
	}
	if state.ToolsFingerprint != effectiveToolsFingerprint {
		return fullPlan(true, "tools_mismatch", state, storeItemCount)
	}
	prefixLen, ok := QoderConversationPrefixLen(state.MessageFingerprints, messageFingerprints)
	if !ok {
		if !options.AppendToExisting || len(messageFingerprints) == 0 {
			return fullPlan(true, "prefix_mismatch", state, storeItemCount)
		}
		committedFingerprints := append([]string(nil), state.MessageFingerprints...)
		matchStatus := "reused_previous_response"
		if QoderConversationHasSuffix(state.MessageFingerprints, messageFingerprints) {
			matchStatus = "reused_previous_response_suffix"
		} else {
			committedFingerprints = append(committedFingerprints, messageFingerprints...)
		}
		return &QoderConversationPlan{
			Store:                 s,
			Key:                   key,
			SessionID:             state.SessionID,
			MessagesToSend:        messages,
			IncludeSystem:         strings.TrimSpace(system) != "",
			IncludeTools:          len(tools) > 0,
			Reused:                true,
			SystemFingerprint:     effectiveSystemFingerprint,
			ToolsFingerprint:      effectiveToolsFingerprint,
			MessageFingerprints:   committedFingerprints,
			CommittedFingerprints: committedFingerprints,
			HasPreviousUsage:      state.HasUsage,
			PreviousUsageInput:    state.LastUsageInput,
			PreviousUsageOutput:   state.LastUsageOutput,
			MatchStatus:           matchStatus,
			PreviousMessageCount:  len(state.MessageFingerprints),
			PrefixMessageCount:    len(state.MessageFingerprints),
			StoreItemCount:        storeItemCount,
			PreviousFirstHash:     FirstQoderFingerprint(state.MessageFingerprints),
			CurrentFirstHash:      currentFirstHash,
			PreviousState:         CloneQoderConversationState(state),
		}
	}
	return &QoderConversationPlan{
		Store:                 s,
		Key:                   key,
		SessionID:             state.SessionID,
		MessagesToSend:        messages,
		IncludeSystem:         true,
		IncludeTools:          true,
		Reused:                true,
		SystemFingerprint:     effectiveSystemFingerprint,
		ToolsFingerprint:      effectiveToolsFingerprint,
		MessageFingerprints:   messageFingerprints,
		CommittedFingerprints: messageFingerprints,
		HasPreviousUsage:      state.HasUsage,
		PreviousUsageInput:    state.LastUsageInput,
		PreviousUsageOutput:   state.LastUsageOutput,
		MatchStatus:           "reused",
		PreviousMessageCount:  len(state.MessageFingerprints),
		PrefixMessageCount:    prefixLen,
		StoreItemCount:        storeItemCount,
		PreviousFirstHash:     FirstQoderFingerprint(state.MessageFingerprints),
		CurrentFirstHash:      currentFirstHash,
		PreviousState:         CloneQoderConversationState(state),
	}
}

func (s *QoderConversationStore) PruneExpiredLocked(now time.Time) {
	if s == nil {
		return
	}
	if len(s.Items) == 0 {
		for alias := range s.Aliases {
			delete(s.Aliases, alias)
		}
		return
	}
	for key, state := range s.Items {
		if state == nil || now.After(state.ExpiresAt) {
			delete(s.Items, key)
		}
	}
	for alias, target := range s.Aliases {
		if _, ok := s.Items[target]; !ok {
			delete(s.Aliases, alias)
		}
	}
}

func (s *QoderConversationStore) ResolveAliasLocked(key string) string {
	if s == nil || strings.TrimSpace(key) == "" {
		return key
	}
	seen := map[string]struct{}{}
	for {
		target := strings.TrimSpace(s.Aliases[key])
		if target == "" || target == key {
			return key
		}
		if _, ok := seen[key]; ok {
			return key
		}
		seen[key] = struct{}{}
		key = target
	}
}

func (s *QoderConversationStore) AddAlias(alias, canonical string) {
	alias = strings.TrimSpace(alias)
	canonical = strings.TrimSpace(canonical)
	if s == nil || alias == "" || canonical == "" || alias == canonical {
		return
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Aliases == nil {
		s.Aliases = make(map[string]string)
	}
	s.Aliases[alias] = s.ResolveAliasLocked(canonical)
}

func (p *QoderConversationPlan) Commit(usages ...upstream.TokenUsage) {
	p.CommitFingerprints(p.CommittedFingerprints, usages...)
}

func (p *QoderConversationPlan) AddAlias(alias string) {
	if p == nil || p.Store == nil || strings.TrimSpace(p.Key) == "" {
		return
	}
	p.Store.AddAlias(alias, p.Key)
}

func (p *QoderConversationPlan) CommitAccepted() {
	if p == nil {
		return
	}
	acceptedState := p.CommitFingerprints(p.AcceptedFingerprints())
	if acceptedState == nil {
		return
	}
	p.AcceptedState = acceptedState
	p.AcceptedCommitted = true
}

func (p *QoderConversationPlan) RollbackAccepted() {
	if p == nil || !p.AcceptedCommitted || p.AcceptedState == nil || p.Store == nil || strings.TrimSpace(p.Key) == "" {
		return
	}
	p.Store.Mu.Lock()
	defer p.Store.Mu.Unlock()
	current := p.Store.Items[p.Key]
	if current == nil {
		return
	}
	// 只回滚本 plan 在 commitAccepted() 中实际写入的 accepted state。
	// 如果其他 goroutine 已经在其后提交了新状态，则 current 会与 acceptedState 不一致，必须放弃回滚。
	if !QoderConversationStateEqual(current, p.AcceptedState) {
		logger.LegacyPrintf("service.qoder_conversation", "WARN: stale rollback abandoned key=%s accepted_version=%d current_version=%d", p.Key, p.AcceptedState.Version, current.Version)
		return
	}
	if p.PreviousState == nil {
		delete(p.Store.Items, p.Key)
		return
	}
	p.Store.Items[p.Key] = CloneQoderConversationState(p.PreviousState)
}

func CloneQoderConversationState(state *QoderConversationState) *QoderConversationState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.MessageFingerprints = append([]string(nil), state.MessageFingerprints...)
	return &cloned
}

func QoderConversationStateEqual(a, b *QoderConversationState) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.SessionID != b.SessionID ||
		a.SystemFingerprint != b.SystemFingerprint ||
		a.ToolsFingerprint != b.ToolsFingerprint ||
		a.HasUsage != b.HasUsage ||
		a.LastUsageInput != b.LastUsageInput ||
		a.LastUsageOutput != b.LastUsageOutput ||
		a.Version != b.Version ||
		!a.ExpiresAt.Equal(b.ExpiresAt) {
		return false
	}
	if len(a.MessageFingerprints) != len(b.MessageFingerprints) {
		return false
	}
	for i := range a.MessageFingerprints {
		if a.MessageFingerprints[i] != b.MessageFingerprints[i] {
			return false
		}
	}
	return true
}

func (p *QoderConversationPlan) AcceptedFingerprints() []string {
	if p == nil {
		return nil
	}
	if !p.Reused {
		return append([]string(nil), p.CommittedFingerprints...)
	}
	prefixLen := p.PrefixMessageCount
	if prefixLen < 0 {
		prefixLen = 0
	}
	if prefixLen > len(p.MessageFingerprints) {
		prefixLen = len(p.MessageFingerprints)
	}
	return append([]string(nil), p.MessageFingerprints[:prefixLen]...)
}

func (p *QoderConversationPlan) CommitFingerprints(fingerprints []string, usages ...upstream.TokenUsage) *QoderConversationState {
	if p == nil || p.Store == nil || strings.TrimSpace(p.Key) == "" {
		return nil
	}
	hasUsage, lastUsageInput, lastUsageOutput := p.PreviousUsageSnapshot()
	if len(usages) > 0 && (usages[0].InputTokens > 0 || usages[0].OutputTokens > 0) {
		hasUsage = true
		lastUsageInput = usages[0].InputTokens
		lastUsageOutput = usages[0].OutputTokens
	}
	p.Store.Mu.Lock()
	defer p.Store.Mu.Unlock()
	if p.Store.Items == nil {
		p.Store.Items = make(map[string]*QoderConversationState)
	}
	committedFingerprints := append([]string(nil), fingerprints...)
	var newVersion int64 = 1
	if existing := p.Store.Items[p.Key]; existing != nil &&
		existing.SessionID == p.SessionID &&
		existing.SystemFingerprint == p.SystemFingerprint &&
		existing.ToolsFingerprint == p.ToolsFingerprint {
		// 继承现有版本号并递增
		newVersion = existing.Version + 1
		if _, ok := QoderConversationPrefixLen(committedFingerprints, existing.MessageFingerprints); ok && len(existing.MessageFingerprints) > len(committedFingerprints) {
			committedFingerprints = append([]string(nil), existing.MessageFingerprints...)
		}
		if existing.HasUsage && (!hasUsage || existing.LastUsageInput > lastUsageInput || existing.LastUsageOutput > lastUsageOutput) {
			hasUsage = true
			if existing.LastUsageInput > lastUsageInput {
				lastUsageInput = existing.LastUsageInput
			}
			if existing.LastUsageOutput > lastUsageOutput {
				lastUsageOutput = existing.LastUsageOutput
			}
		}
	}
	next := &QoderConversationState{
		SessionID:           p.SessionID,
		SystemFingerprint:   p.SystemFingerprint,
		ToolsFingerprint:    p.ToolsFingerprint,
		MessageFingerprints: committedFingerprints,
		HasUsage:            hasUsage,
		LastUsageInput:      lastUsageInput,
		LastUsageOutput:     lastUsageOutput,
		ExpiresAt:           time.Now().Add(p.Store.Ttl),
		Version:             newVersion,
	}
	p.Store.Items[p.Key] = next
	return CloneQoderConversationState(next)
}

func (p *QoderConversationPlan) PreviousUsageSnapshot() (bool, int, int) {
	if p == nil {
		return false, 0, 0
	}
	hasUsage := p.HasPreviousUsage
	lastUsageInput := p.PreviousUsageInput
	lastUsageOutput := p.PreviousUsageOutput
	if p.Store == nil || strings.TrimSpace(p.Key) == "" {
		return hasUsage, lastUsageInput, lastUsageOutput
	}
	// 访问 store.items 必须持有锁，避免与 commitFingerprints 的写操作竞态
	p.Store.Mu.Lock()
	existing := p.Store.Items[p.Key]
	p.Store.Mu.Unlock()
	if existing != nil &&
		existing.SessionID == p.SessionID &&
		existing.SystemFingerprint == p.SystemFingerprint &&
		existing.ToolsFingerprint == p.ToolsFingerprint &&
		existing.HasUsage {
		hasUsage = true
		if existing.LastUsageInput > lastUsageInput {
			lastUsageInput = existing.LastUsageInput
		}
		if existing.LastUsageOutput > lastUsageOutput {
			lastUsageOutput = existing.LastUsageOutput
		}
	}
	return hasUsage, lastUsageInput, lastUsageOutput
}

func (p *QoderConversationPlan) Log(metadata RequestMetadata, accountID int64, protocol, model, keySource string, request QoderPayloadRequest, payload map[string]any) {
	if p == nil {
		return
	}

	originalToolsBytes := QoderJSONSize(request.Tools)
	sentTools := QoderAnySlice(payload["tools"])
	sentToolsBytes := QoderJSONSize(sentTools)
	systemBytes := len([]byte(request.System))
	sentSystemBytes := 0
	if p.IncludeSystem {
		sentSystemBytes = systemBytes
	}
	p.Diagnostics = QoderConversationDiagnostics{
		Protocol:             protocol,
		Model:                model,
		KeySource:            keySource,
		RequestID:            QoderStringField(payload, "request_id"),
		OriginalMessages:     len(request.Messages),
		SentMessages:         len(p.MessagesToSend),
		OriginalToolsCount:   len(request.Tools),
		SentToolsCount:       len(sentTools),
		OriginalToolsBytes:   originalToolsBytes,
		SentToolsBytes:       sentToolsBytes,
		SystemBytes:          systemBytes,
		SentSystemBytes:      sentSystemBytes,
		OutboundPayloadBytes: QoderJSONSize(payload),
	}
	logger.L().Info("qoder session",
		zap.String("protocol", protocol),
		zap.String("model", model),
		zap.String("key_source", keySource),
		zap.String("key_hash", upstream.HashSensitiveValueForLog(p.Key)),
		zap.String("match_status", p.MatchStatus),
		zap.Int64("account_id", accountID),
		zap.Int64("api_key_id", metadata.APIKeyID),
		zap.String("request_id", p.Diagnostics.RequestID),
		zap.Bool("reused", p.Reused),
		zap.Bool("used_full_replay", !p.Reused),
		zap.Bool("include_system", p.IncludeSystem),
		zap.Bool("include_tools", p.IncludeTools),
		zap.Int("store_item_count", p.StoreItemCount),
		zap.Int("previous_messages", p.PreviousMessageCount),
		zap.Int("prefix_messages", p.PrefixMessageCount),
		zap.Int("original_messages", p.Diagnostics.OriginalMessages),
		zap.Int("sent_messages", p.Diagnostics.SentMessages),
		zap.String("previous_first_message_hash", p.PreviousFirstHash),
		zap.String("current_first_message_hash", p.CurrentFirstHash),
		zap.Bool("first_message_hash_changed", p.PreviousFirstHash != "" && p.CurrentFirstHash != "" && p.PreviousFirstHash != p.CurrentFirstHash),
		zap.Bool("system_changed", p.MatchStatus == "system_mismatch"),
		zap.Bool("tools_changed", p.MatchStatus == "tools_mismatch"),
		zap.Int("original_tools_count", p.Diagnostics.OriginalToolsCount),
		zap.Int("sent_tools_count", p.Diagnostics.SentToolsCount),
		zap.Int("original_tools_bytes", p.Diagnostics.OriginalToolsBytes),
		zap.Int("sent_tools_bytes", p.Diagnostics.SentToolsBytes),
		zap.Int("system_bytes", p.Diagnostics.SystemBytes),
		zap.Int("sent_system_bytes", p.Diagnostics.SentSystemBytes),
		zap.Int("outbound_payload_bytes", p.Diagnostics.OutboundPayloadBytes),
	)
}

func (p *QoderConversationPlan) RecordUsage(upstreamUsage upstream.TokenUsage) upstream.TokenUsage {
	return upstreamUsage
}

func (p *QoderConversationPlan) PreviousUsageForRecord() (int, int, bool) {
	if p == nil {
		return 0, 0, false
	}
	input := p.PreviousUsageInput
	output := p.PreviousUsageOutput
	hasPrevious := p.HasPreviousUsage
	if p.Store == nil || strings.TrimSpace(p.Key) == "" {
		return input, output, hasPrevious
	}
	p.Store.Mu.Lock()
	defer p.Store.Mu.Unlock()
	state := p.Store.Items[p.Key]
	if state == nil || !state.HasUsage || state.SessionID != p.SessionID {
		return input, output, hasPrevious
	}
	if _, ok := QoderConversationPrefixLen(state.MessageFingerprints, p.MessageFingerprints); !ok {
		return input, output, hasPrevious
	}
	if state.LastUsageInput > input {
		input = state.LastUsageInput
	}
	if state.LastUsageOutput > output {
		output = state.LastUsageOutput
	}
	return input, output, true
}

func (p *QoderConversationPlan) ShouldTreatUsageAsCumulative(usage upstream.TokenUsage, previousInput int, hasPrevious bool) bool {
	if p == nil || !p.Reused || !hasPrevious || previousInput <= 0 || usage.InputTokens <= previousInput {
		return false
	}
	diag := p.Diagnostics
	return diag.SentMessages < diag.OriginalMessages || diag.SentToolsBytes < diag.OriginalToolsBytes || diag.SentSystemBytes < diag.SystemBytes
}

func (p *QoderConversationPlan) LogUsage(metadata RequestMetadata, accountID int64, upstreamUsage upstream.TokenUsage, recordUsage upstream.TokenUsage) {
	if p == nil {
		return
	}

	diag := p.Diagnostics
	previousInput, previousOutput, hasPreviousUsage := p.PreviousUsageForRecord()
	upstreamUsageCumulativeSuspected := p.ShouldTreatUsageAsCumulative(upstreamUsage, previousInput, hasPreviousUsage)
	logger.L().Info("qoder usage",
		zap.String("protocol", diag.Protocol),
		zap.String("model", diag.Model),
		zap.String("key_source", diag.KeySource),
		zap.Int64("account_id", accountID),
		zap.Int64("api_key_id", metadata.APIKeyID),
		zap.String("request_id", diag.RequestID),
		zap.Bool("reused", p.Reused),
		zap.Bool("used_full_replay", !p.Reused),
		zap.Bool("include_system", p.IncludeSystem),
		zap.Bool("include_tools", p.IncludeTools),
		zap.Int("original_messages", diag.OriginalMessages),
		zap.Int("sent_messages", diag.SentMessages),
		zap.Int("sent_tools_bytes", diag.SentToolsBytes),
		zap.Int("sent_system_bytes", diag.SentSystemBytes),
		zap.Int("outbound_payload_bytes", diag.OutboundPayloadBytes),
		zap.String("usage_source", "qoder_sse"),
		zap.Int("upstream_usage_input_tokens", upstreamUsage.InputTokens),
		zap.Int("upstream_usage_output_tokens", upstreamUsage.OutputTokens),
		zap.Int("recorded_usage_input_tokens", recordUsage.InputTokens),
		zap.Int("recorded_usage_output_tokens", recordUsage.OutputTokens),
		zap.Bool("has_previous_upstream_usage", hasPreviousUsage),
		zap.Int("previous_upstream_usage_input_tokens", previousInput),
		zap.Int("previous_upstream_usage_output_tokens", previousOutput),
		zap.Bool("wire_payload_reduced", p.Reused && (diag.SentMessages < diag.OriginalMessages || diag.SentToolsBytes < diag.OriginalToolsBytes || diag.SentSystemBytes < diag.SystemBytes)),
		zap.Bool("upstream_usage_cumulative_suspected", upstreamUsageCumulativeSuspected),
	)
}

func QoderConversationKey(metadata RequestMetadata, accountID int64, protocol string, request QoderPayloadRequest) (string, string) {
	if value := strings.TrimSpace(request.ExplicitSession); value != "" && !request.AutoResponseSession {
		return QoderAccountScopedConversationKey(accountID, QoderConversationExplicitSessionKey(metadata, value)), "body_session"
	}
	if value := strings.TrimSpace(request.PromptCacheKey); value != "" {
		return QoderAccountScopedConversationKey(accountID, "prompt_cache_key:"+upstream.IsolateSessionID(metadata.APIKeyID, value)), "prompt_cache_key"
	}
	if parsed := protocolanthropic.ParseMetadataUserID(request.MetadataUserID); parsed != nil && strings.TrimSpace(parsed.SessionID) != "" {
		return QoderAccountScopedConversationKey(accountID, "metadata_user_id:"+upstream.IsolateSessionID(metadata.APIKeyID, parsed.SessionID)), "metadata_user_id"
	}
	if value := QoderHeaderSessionID(metadata.Headers); value != "" {
		return QoderAccountScopedConversationKey(accountID, "header:"+upstream.IsolateSessionID(metadata.APIKeyID, value)), "header"
	}
	if metadata.ClaudeCode {
		if value := QoderClaudeCodeStablePrefixKey(request); value != "" {
			return QoderAccountScopedConversationKey(accountID, "claude_code_prefix:"+upstream.IsolateSessionID(metadata.APIKeyID, value)), "claude_code_prefix"
		}
	}
	if value := strings.TrimSpace(request.ExplicitSession); value != "" {
		return QoderAccountScopedConversationKey(accountID, QoderConversationExplicitSessionKey(metadata, value)), "body_session"
	}
	return QoderAccountScopedConversationKey(accountID, "request:"+uuid.NewString()), "request"
}

func QoderAccountScopedConversationKey(accountID int64, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || accountID <= 0 {
		return key
	}
	return fmt.Sprintf("account:%d:%s", accountID, key)
}

func QoderConversationExplicitSessionKey(metadata RequestMetadata, value string) string {
	return "body_session:" + upstream.IsolateSessionID(metadata.APIKeyID, value)
}

func QoderHeaderSessionID(headers http.Header) string {
	if headers == nil {
		return ""
	}
	for _, header := range []string{"session_id", "conversation_id", "x-session-id", "x-conversation-id", "X-Claude-Code-Session-Id"} {
		if value := strings.TrimSpace(headers.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

func QoderConversationPrefixLen(previous, current []string) (int, bool) {
	if len(previous) > len(current) {
		return 0, false
	}
	for i := range previous {
		if previous[i] != current[i] {
			return 0, false
		}
	}
	return len(previous), true
}

func QoderConversationHasSuffix(full, suffix []string) bool {
	if len(suffix) == 0 || len(suffix) > len(full) {
		return false
	}
	offset := len(full) - len(suffix)
	for i := range suffix {
		if full[offset+i] != suffix[i] {
			return false
		}
	}
	return true
}

func FirstQoderFingerprint(fingerprints []string) string {
	if len(fingerprints) == 0 {
		return ""
	}
	return fingerprints[0]
}

func QoderJSONSize(value any) int {
	body, err := json.Marshal(value)
	if err != nil {
		return len([]byte(fmt.Sprint(value)))
	}
	return len(body)
}

func QoderSessionIDForConversation(key, systemFingerprint, toolsFingerprint string, messageFingerprints []string) string {
	var b strings.Builder
	_, _ = b.WriteString(key)
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(systemFingerprint)
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(toolsFingerprint)
	_, _ = b.WriteString("::")
	for _, fingerprint := range messageFingerprints {
		_, _ = b.WriteString(fingerprint)
		_, _ = b.WriteString(",")
	}
	return upstream.GenerateSessionUUID(b.String())
}
