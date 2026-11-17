package qoder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// conversation.go plans reusable Qoder conversations and publishes or rolls
// back versioned history and usage snapshots.
// Conversation reuse depends on a provider-scoped key plus matching system,
// tools, and message fingerprints. A plan records the previous snapshot before
// inference, and acceptance publishes a versioned state so a later read or
// output failure can restore the snapshot belonging to that attempt.
//
// The store replaces state pointers under its mutex. A rollback compares the
// complete accepted state, including version and expiry, before restoring or
// deleting it; a competing commit therefore makes an older rollback stale.
// Commits retain a longer compatible history and the highest observed token
// counts. Clients supply every conversation turn, so no input text or
// tool output needs to survive in the store after request completion.

// RequestMetadata carries caller isolation and inbound headers used for
// session-key selection.
type RequestMetadata struct {
	APIKeyID   int64 // inbound API-key identifier
	ClaudeCode bool  // Claude Code marker
	// Headers borrows inbound metadata retained by the caller. Session-key
	// selection reads it synchronously, so callers coordinate changes with
	// planning.
	Headers http.Header // inbound request headers
}

// QoderConversationStore holds versioned fingerprints and usage observations under Mu.
type QoderConversationStore struct {
	// Mu protects Ttl and Items during concurrent store use. Plans prune,
	// resolve, publish, and roll back under this mutex. Treat stored state pointers
	// and their fingerprint slices as immutable after publication; commits replace
	// them and copy fingerprints for retained snapshots.
	Mu    sync.Mutex                         // conversation map mutex
	Ttl   time.Duration                      // stored state lifetime
	Items map[string]*QoderConversationState // canonical conversation states
}

// QoderConversationState is a published session-history and usage snapshot
// retained until expiry.
type QoderConversationState struct {
	SessionID           string   // upstream session identifier
	SystemFingerprint   string   // normalized system hash
	ToolsFingerprint    string   // serialized tool hash
	MessageFingerprints []string // ordered history hashes
	// HasUsage records that at least one input or output count was positive.
	// LastUsageInput and LastUsageOutput retain the selected observations; a
	// companion count can be zero or negative. Compatible commits keep larger
	// concurrent values while planning captures the snapshot.
	HasUsage        bool      // positive usage observed
	LastUsageInput  int       // input tokens
	LastUsageOutput int       // output tokens
	ExpiresAt       time.Time // state expiry time
	Version         int64     // accepted publication version
}

// QoderConversationPlan holds one attempt's reuse decision and acceptance
// rollback snapshots. Each attempt owns its plan and calls mutation and
// diagnostic methods serially; concurrent attempts share the store.
type QoderConversationPlan struct {
	// Store and Key identify the publication target; nil Store or blank Key
	// disables state writes. SessionID carries the upstream identity selected by
	// planning for that target.
	Store     *QoderConversationStore // owning conversation store
	Key       string                  // canonical conversation key
	SessionID string                  // upstream session identifier
	// MessagesToSend borrows the request's message slice. Payload construction can
	// read and mutate contained text blocks, so callers coordinate sharing with
	// preparation.
	MessagesToSend    []QoderMessage // outgoing request messages
	IncludeSystem     bool           // send system prompt
	IncludeTools      bool           // send tool definitions
	Reused            bool           // saved session reused
	Fallback          bool           // full replay fallback
	SystemFingerprint string         // normalized system hash
	ToolsFingerprint  string         // serialized tool hash
	// MessageFingerprints and CommittedFingerprints can share the planning slice.
	// Acceptance uses the selected prefix, while completion uses the committed
	// sequence; store publication copies either sequence before retaining it.
	MessageFingerprints   []string // ordered history hashes
	CommittedFingerprints []string // completion history hashes
	// HasPreviousUsage, PreviousUsageInput, and PreviousUsageOutput capture the
	// planning snapshot. PreviousUsageSnapshot can combine them with later
	// compatible stored observations before another publication.
	HasPreviousUsage     bool                         // captured usage available
	PreviousUsageInput   int                          // previous input tokens
	PreviousUsageOutput  int                          // previous output tokens
	MatchStatus          string                       // reuse decision label
	PreviousMessageCount int                          // saved history count
	PrefixMessageCount   int                          // matched prefix count
	StoreItemCount       int                          // planning state count
	PreviousFirstHash    string                       // saved first-message hash
	CurrentFirstHash     string                       // current first-message hash
	Diagnostics          QoderConversationDiagnostics // payload logging observations
	// PreviousState is cloned during planning and supplies the restoration target.
	// Rollback installs it only when the accepted publication still matches current
	// state.
	PreviousState *QoderConversationState // pre-attempt state snapshot
	// AcceptedState is the cloned publication returned by CommitAccepted. Rollback
	// compares it with current state to reject a later competing commit.
	AcceptedState *QoderConversationState // accepted publication snapshot
	// AcceptedCommitted records whether CommitAccepted published AcceptedState.
	// RollbackAccepted requires both before considering PreviousState, preventing
	// an unaccepted plan from restoring captured history.
	AcceptedCommitted bool // acceptance publication marker
}

// QoderConversationDiagnostics records payload sizes and identity sources for
// session and usage logs.
type QoderConversationDiagnostics struct {
	Protocol             string // inbound protocol label
	Model                string // client model name
	KeySource            string // session identifier source
	RequestID            string // outgoing request identifier
	OriginalMessages     int    // received message count
	SentMessages         int    // outgoing message count
	OriginalToolsCount   int    // received tool count
	SentToolsCount       int    // outgoing tool count
	OriginalToolsBytes   int    // received tool bytes
	SentToolsBytes       int    // outgoing tool bytes
	SystemBytes          int    // received system bytes
	SentSystemBytes      int    // outgoing system bytes
	OutboundPayloadBytes int    // complete payload bytes
}

// NewQoderConversationStore allocates the conversation fingerprint map with the
// supplied TTL. Nonpositive TTL selects QoderConversationTTL; expiration is
// checked when planning rather than by a background cleanup goroutine.
func NewQoderConversationStore(ttl time.Duration) *QoderConversationStore {
	if ttl <= 0 {
		ttl = QoderConversationTTL
	}
	return &QoderConversationStore{
		Ttl:   ttl,
		Items: make(map[string]*QoderConversationState),
	}
}

// Plan selects a session and captures an immutable snapshot for later commits.
// A nil store or blank key disables reuse and creates a random session.
// Otherwise planning prunes expired state, resolves aliases, and compares
// system, tool, and message fingerprints while holding the conversation mutex.
//
// Matching prefixes retain session identity while sending the complete supplied
// history. Missing conversation input is restored before planning, so fingerprints
// never stand in for omitted instructions, tools, or messages. Mismatches select
// a new full-replay session and preserve the previous snapshot for rollback;
// acceptance and completion retain their existing version checks.
func (s *QoderConversationStore) Plan(key, system string, tools []any, messages []QoderMessage) *QoderConversationPlan {
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
	// Disabled reuse has no stable store entry to publish or restore. Give this
	// request a random session and detach the plan from shared state.
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
	s.PruneExpiredLocked(now)
	storeItemCount := len(s.Items)
	state := s.Items[key]
	if state == nil {
		return fullPlan(false, "no_state", nil, storeItemCount)
	}
	if now.After(state.ExpiresAt) {
		delete(s.Items, key)
		return fullPlan(true, "expired", state, storeItemCount)
	}
	if state.SystemFingerprint != systemFingerprint {
		return fullPlan(true, "system_mismatch", state, storeItemCount)
	}
	if state.ToolsFingerprint != toolsFingerprint {
		return fullPlan(true, "tools_mismatch", state, storeItemCount)
	}
	prefixLen, ok := QoderConversationPrefixLen(state.MessageFingerprints, messageFingerprints)
	if !ok {
		return fullPlan(true, "prefix_mismatch", state, storeItemCount)
	}
	// Prefix matching chooses compatible session identity. Retain the entire
	// submitted history and its system/tools flags here because this branch does
	// not select a reduced outgoing payload.
	return &QoderConversationPlan{
		Store:                 s,
		Key:                   key,
		SessionID:             state.SessionID,
		MessagesToSend:        messages,
		IncludeSystem:         true,
		IncludeTools:          true,
		Reused:                true,
		SystemFingerprint:     systemFingerprint,
		ToolsFingerprint:      toolsFingerprint,
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

// PruneExpiredLocked removes expired fingerprint and usage observations. Callers
// hold Mu throughout the sweep so a concurrent request cannot extend a session
// while its previous deadline is being examined.
func (s *QoderConversationStore) PruneExpiredLocked(now time.Time) {
	if s == nil {
		return
	}
	for key, state := range s.Items {
		if state == nil || now.After(state.ExpiresAt) {
			delete(s.Items, key)
		}
	}
}

// Commit publishes a nonnil plan's completion fingerprints and optional first
// usage snapshot. Disabled plans are handled by CommitFingerprints, which owns
// versioning, compatible-history merging, and stored expiry renewal; a nil
// plan panics before that delegation.
func (p *QoderConversationPlan) Commit(usages ...upstream.TokenUsage) {
	p.CommitFingerprints(p.CommittedFingerprints, usages...)
}

// CommitAccepted publishes the fingerprints appropriate to transport acceptance
// and saves its returned snapshot. Disabled or nil plans produce no accepted
// state. The saved snapshot and flag let RollbackAccepted identify this
// attempt's publication if later stream reading fails.
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

// RollbackAccepted restores the pre-attempt state when this plan still owns the
// accepted publication. Nil, disabled, or unaccepted plans are ignored, and the
// state comparison runs under the store mutex.
//
// The current state must equal the accepted snapshot in session, fingerprints,
// usage, expiry, and version. A later commit changes that snapshot and causes
// rollback to be skipped. An accepted new conversation is deleted; an existing
// conversation is replaced with a clone of PreviousState.
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
	// Restore only the publication saved by CommitAccepted. A later commit changes
	// its version or other state, so this comparison leaves that competing work
	// intact.
	if !QoderConversationStateEqual(current, p.AcceptedState) {
		logger.LegacyPrintf("service.qoder_conversation", "WARN: stale rollback abandoned key=%s accepted_version=%d current_version=%d", p.Key, p.AcceptedState.Version, current.Version)
		return
	}
	// Equality established that this acceptance is still current. Delete a newly
	// created conversation or restore its captured predecessor so failed work
	// cannot remain the continuation target.
	if p.PreviousState == nil {
		delete(p.Store.Items, p.Key)
		return
	}
	p.Store.Items[p.Key] = CloneQoderConversationState(p.PreviousState)
}

// CloneQoderConversationState copies a state and its message-fingerprint slice,
// returning nil for nil input. The returned snapshot can be retained across
// later store replacements; strings and value fields need no additional copying.
func CloneQoderConversationState(state *QoderConversationState) *QoderConversationState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.MessageFingerprints = append([]string(nil), state.MessageFingerprints...)
	return &cloned
}

// QoderConversationStateEqual compares complete state snapshots, including
// version, expiry, and message order. Two nil states are equal; callers use this
// comparison to reject rollback when a later publication changed any recorded
// state.
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

// AcceptedFingerprints returns a copied history appropriate to this plan's
// transport acceptance. A new plan uses all completion fingerprints; reused
// plans keep the bounded saved prefix, leaving later completion to publish the
// full history.
func (p *QoderConversationPlan) AcceptedFingerprints() []string {
	if p == nil {
		return nil
	}
	if !p.Reused {
		return append([]string(nil), p.CommittedFingerprints...)
	}
	// Clamp the saved prefix count before slicing acceptance history.
	prefixLen := min(max(p.PrefixMessageCount, 0), len(p.MessageFingerprints))
	return append([]string(nil), p.MessageFingerprints[:prefixLen]...)
}

// CommitFingerprints publishes a versioned state and returns an independent
// snapshot of it. Nil or disabled plans return nil. The first supplied usage
// updates the snapshot only when its input or output count is positive;
// otherwise previous usage remains available.
//
// Matching session/system/tools state advances the version, retains an existing
// longer compatible history, and keeps maximum observed input/output counts.
// Fingerprints are copied before publication and expiry renews from the store
// TTL. This prevents a shorter concurrent commit from erasing accumulated
// history and invalidates older acceptance rollbacks.
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
		// A compatible commit advances the publication version before replacing the
		// state. Earlier accepted snapshots then fail rollback equality even for
		// identical history.
		newVersion = existing.Version + 1
		// A competing compatible plan may have published a longer history. Keep that
		// extension when this commit's fingerprints are its prefix so progress is not
		// shortened.
		if _, ok := QoderConversationPrefixLen(committedFingerprints, existing.MessageFingerprints); ok && len(existing.MessageFingerprints) > len(committedFingerprints) {
			committedFingerprints = append([]string(nil), existing.MessageFingerprints...)
		}
		// Compatible attempts can finish with different usage observations. Retain
		// the larger count in each category so a later shorter result cannot erase
		// the diagnostic snapshot.
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

// PreviousUsageSnapshot combines the plan's captured usage with current
// compatible store observations. Nil plans return no usage. Disabled plans
// retain their captured values, while enabled plans read the current state
// pointer under Mu.
//
// Stored states are replaced rather than mutated by commit operations, so the
// retrieved pointer can be examined after unlocking. A matching session, system,
// and tool identity contributes its maximum input and output counts. A different
// identity contributes nothing to this plan's usage snapshot.
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
	// CommitFingerprints replaces state pointers under Mu. Read that pointer under
	// the same mutex, then inspect the immutable published snapshot after releasing
	// it.
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

// Log records payload sizes and conversation decisions for this plan's
// diagnostics. Nil plans are ignored. It stores the computed diagnostics on the
// plan and hashes the conversation key before logging request metadata.
func (p *QoderConversationPlan) Log(metadata RequestMetadata, providerID int64, protocol, model, keySource string, request QoderPayloadRequest, payload map[string]any) {
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
		zap.Int64("provider_id", providerID),
		zap.Int64("api_key_id", metadata.APIKeyID),
		zap.String("request_id", p.Diagnostics.RequestID),
		zap.Bool("reused", p.Reused),
		zap.Bool("used_full_replay", p.IncludeSystem && p.IncludeTools && len(p.MessagesToSend) >= p.Diagnostics.OriginalMessages),
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

// RecordUsage returns the supplied upstream token usage unchanged for completion
// accounting.
func (p *QoderConversationPlan) RecordUsage(upstreamUsage upstream.TokenUsage) upstream.TokenUsage {
	return upstreamUsage
}

// PreviousUsageForRecord reads the captured usage and any current compatible
// history under Mu. Nil plans report no previous usage; disabled plans return
// their captured values.
//
// Current state must have usage, match the session ID, and form a prefix of the
// plan's message fingerprints. Only then are larger stored input/output counts
// considered. These observations support logging heuristics and do not subtract
// prior counts from the usage returned by RecordUsage.
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

// ShouldTreatUsageAsCumulative reports a logging suspicion when session reuse
// combines a reduced payload with rising input usage. Nil or unreused plans,
// absent previous usage, and nonpositive previous input return false. A larger
// new input count qualifies only when sent messages, tool bytes, or system
// bytes decreased from the request.
//
// LogUsage reports this flag with the counts used to derive it. The heuristic
// observes payload reduction; RecordUsage returns the supplied usage
// unchanged, so this decision contributes diagnostics rather than a
// subtraction from completion accounting.
func (p *QoderConversationPlan) ShouldTreatUsageAsCumulative(usage upstream.TokenUsage, previousInput int, hasPrevious bool) bool {
	if p == nil || !p.Reused || !hasPrevious || previousInput <= 0 || usage.InputTokens <= previousInput {
		return false
	}
	diag := p.Diagnostics
	return diag.SentMessages < diag.OriginalMessages || diag.SentToolsBytes < diag.OriginalToolsBytes || diag.SentSystemBytes < diag.SystemBytes
}

// LogUsage records upstream and recorded token counts with conversation
// diagnostics. Nil plans are ignored. PreviousUsageForRecord and the reduction
// heuristic add diagnostic context, while recorded usage values remain supplied
// by the execution caller.
func (p *QoderConversationPlan) LogUsage(metadata RequestMetadata, providerID int64, upstreamUsage upstream.TokenUsage, recordUsage upstream.TokenUsage) {
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
		zap.Int64("provider_id", providerID),
		zap.Int64("api_key_id", metadata.APIKeyID),
		zap.String("request_id", diag.RequestID),
		zap.Bool("reused", p.Reused),
		zap.Bool("used_full_replay", p.IncludeSystem && p.IncludeTools && len(p.MessagesToSend) >= diag.OriginalMessages),
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

// QoderConversationKey selects a provider-scoped key and names the source used
// for reuse. Explicit body sessions win, followed by prompt-cache keys, Anthropic
// metadata session IDs, headers and Claude Code stable prefixes.
//
// API-key isolation is applied to every client-supplied session source, and
// provider scoping keeps the same caller session separate across accounts. A
// request lacking a stable source leaves the lookup key empty. The planner
// creates a fresh upstream session without retaining unreachable fingerprints.
func QoderConversationKey(metadata RequestMetadata, providerID int64, request QoderPayloadRequest) (string, string) {
	if value := strings.TrimSpace(request.ExplicitSession); value != "" {
		return QoderProviderScopedConversationKey(providerID, QoderConversationExplicitSessionKey(metadata, value)), "body_session"
	}
	if value := strings.TrimSpace(request.PromptCacheKey); value != "" {
		return QoderProviderScopedConversationKey(providerID, "prompt_cache_key:"+upstream.IsolateSessionID(metadata.APIKeyID, value)), "prompt_cache_key"
	}
	if parsed := protocolanthropic.ParseMetadataUserID(request.MetadataUserID); parsed != nil && strings.TrimSpace(parsed.SessionID) != "" {
		return QoderProviderScopedConversationKey(providerID, "metadata_user_id:"+upstream.IsolateSessionID(metadata.APIKeyID, parsed.SessionID)), "metadata_user_id"
	}
	if value := QoderHeaderSessionID(metadata.Headers); value != "" {
		return QoderProviderScopedConversationKey(providerID, "header:"+upstream.IsolateSessionID(metadata.APIKeyID, value)), "header"
	}
	if metadata.ClaudeCode {
		if value := QoderClaudeCodeStablePrefixKey(request); value != "" {
			return QoderProviderScopedConversationKey(providerID, "claude_code_prefix:"+upstream.IsolateSessionID(metadata.APIKeyID, value)), "claude_code_prefix"
		}
	}
	return "", "request"
}

// QoderProviderScopedConversationKey prefixes a trimmed key with a positive
// provider identifier. Blank keys or nonpositive provider IDs return the trimmed
// key, allowing disabled or unscoped callers to retain their supplied
// identifier.
func QoderProviderScopedConversationKey(providerID int64, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || providerID <= 0 {
		return key
	}
	return fmt.Sprintf("account:%d:%s", providerID, key)
}

// QoderConversationExplicitSessionKey labels the API-key isolation digest of a
// body session value. IsolateSessionID trims the value; blank input produces
// the body_session label with an empty digest for the conversation caller to
// handle.
func QoderConversationExplicitSessionKey(metadata RequestMetadata, value string) string {
	return "body_session:" + upstream.IsolateSessionID(metadata.APIKeyID, value)
}

// QoderHeaderSessionID returns the first nonempty recognized inbound
// conversation header. Header values are trimmed and checked in the fixed
// session/conversation order; nil headers or absent recognized values return
// empty text.
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

// QoderConversationPrefixLen reports whether previous is an ordered prefix of
// current. An empty previous slice matches with length zero, while longer or
// differing histories return false for the planner's replay decision.
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

// FirstQoderFingerprint returns the first ordered history hash for
// diagnostics. An absent or empty slice returns empty text so logs can
// distinguish a missing comparison endpoint from a present message.
func FirstQoderFingerprint(fingerprints []string) string {
	if len(fingerprints) == 0 {
		return ""
	}
	return fingerprints[0]
}

// QoderJSONSize returns the byte length of compact JSON for diagnostic sizing.
// Unmarshalable values use their formatted text length, allowing diagnostic
// logging to continue even when a custom value cannot be encoded.
func QoderJSONSize(value any) int {
	body, err := json.Marshal(value)
	if err != nil {
		// Diagnostic sizing must still return a number for custom values that cannot
		// be encoded. Measure their formatted text for this logging observation.
		return len([]byte(fmt.Sprint(value)))
	}
	return len(body)
}

// QoderSessionIDForConversation derives a deterministic UUID from the key and
// ordered fingerprints. System, tools, and messages participate in the joined
// input, so a full-replay plan can identify the same normalized request history.
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
