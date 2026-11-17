package qoder

import (
	"encoding/json"
	"strings"
	"time"
)

// response_history.go retains bounded Responses replay contexts in memory.
// The current Qoder protocol expects full history even when its session ID is
// reused. A caller supplying previous_response_id can omit earlier messages,
// so successful Responses output must be joined with its input before another
// turn is sent upstream. Chat and Messages callers supply their own history.
//
// Each response ID owns an immutable serialized context scoped to its
// authenticated API key. This keeps forks from reading a newer conversation head and
// avoids sharing mutable message maps with later requests. The conversation
// mutex protects publication, expiration, and total-byte accounting. Histories
// exceeding the per-entry limit are not retained; oldest entries are evicted
// when count or total size reaches its bound. Expired or missing partial
// continuations fail explicitly instead of sending incomplete context upstream.

// Replay bounds limit retained user conversation data independently of model
// context capacity. They apply to serialized history, including tool definitions.
const (
	qoderResponseHistoryLimit      = 128
	qoderResponseHistoryEntryBytes = 32 << 20
	qoderResponseHistoryTotalBytes = 128 << 20
)

// qoderResponseHistoryEntry owns immutable history bytes and their expiration.
type qoderResponseHistoryEntry struct {
	Payload   []byte    // serialized replay context
	ExpiresAt time.Time // reusable history deadline
}

// qoderResponseHistory separates submitted input from generated assistant output.
// Input permits recognition of clients resending full transcripts, while Output
// supplies calls and text omitted by ordinary previous_response_id continuations.
type qoderResponseHistory struct {
	System string         // retained instruction text
	Tools  []any          // retained tool definitions
	Input  []QoderMessage // complete submitted history
	Output []QoderMessage // generated visible text and calls
}

// RestoreResponseHistory supplies missing history from an exact prior response.
// Requests without a previous ID are unchanged. A missing entry rejects partial
// input, while complete submitted history can proceed independently.
//
// A longer request beginning with the previous submitted input is already a
// full replay and remains authoritative. Other input is appended after the
// saved assistant output. Empty instructions and tool definitions inherit their
// prior values, preserving existing continuation behavior without requiring the
// remote session to remember any omitted prompt content.
func (s *QoderConversationStore) RestoreResponseHistory(metadata RequestMetadata, request QoderPayloadRequest) (QoderPayloadRequest, error) {
	if request.PreviousResponseID == "" {
		return request, nil
	}
	key := qoderResponseHistoryKey(metadata, request.PreviousResponseID)
	var history qoderResponseHistory
	found := false
	if s != nil {
		s.Mu.Lock()
		s.pruneResponseHistoriesLocked(time.Now())
		entry, exists := s.responseHistories[key]
		s.Mu.Unlock()
		found = exists && json.Unmarshal(entry.Payload, &history) == nil
	}
	if !found {
		complete := false
		for _, message := range request.Messages {
			complete = complete || message.Role == "assistant"
		}
		if request.RequiresPreviousResponse || !complete {
			return request, &RequestError{Code: "previous_response_unavailable", Message: "The previous Qoder response is unavailable; include the complete conversation history"}
		}
		return request, nil
	}
	previous := QoderMessageFingerprints(history.Input)
	current := QoderMessageFingerprints(request.Messages)
	prefix, matches := QoderConversationPrefixLen(previous, current)
	fullReplay := matches && prefix == len(previous) && len(current) > len(previous)
	if !fullReplay {
		// The saved assistant calls must precede newly supplied tool outputs. Each
		// history decode owns its maps, so appending cannot alter another response ID.
		messages := append(history.Input, history.Output...)
		request.Messages = append(messages, request.Messages...)
	}
	if strings.TrimSpace(request.System) == "" {
		request.System = history.System
	}
	if len(request.Tools) == 0 {
		request.Tools = history.Tools
	}
	request.RequiresPreviousResponse = false
	request.Messages = EnrichQoderToolResultMessages(request.Messages)
	return request, nil
}

// SaveResponseHistory publishes a completed response's immutable replay context.
// An explicit storage opt-out skips serialization. Empty output is valid,
// while malformed output and oversized histories cannot enter the cache. The response still remains successful for its current caller.
//
// Serialization precedes the mutex so large user histories do not block reads.
// Publication prunes expired entries and evicts oldest contexts until both
// bounds admit the new value. Existing response IDs are never replaced, keeping
// forks stable even if a caller accidentally attempts duplicate publication.
func (s *QoderConversationStore) SaveResponseHistory(metadata RequestMetadata, request QoderPayloadRequest, output json.RawMessage) {
	if s == nil || request.DisableResponseStorage || request.ResponseID == "" {
		return
	}
	if len(output) == 0 {
		output = json.RawMessage(`[]`)
	}
	assistant, err := QoderMessagesFromResponsesInput(output)
	if err != nil {
		return
	}
	history := qoderResponseHistory{System: request.System, Tools: request.Tools, Input: request.Messages, Output: assistant}
	payload, err := json.Marshal(history)
	if err != nil || len(payload) > qoderResponseHistoryEntryBytes {
		// Retention is bounded independently of a successful inference response.
		// A later partial continuation will report that its history is unavailable.
		return
	}
	key := qoderResponseHistoryKey(metadata, request.ResponseID)
	now := time.Now()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.pruneResponseHistoriesLocked(now)
	if s.responseHistories == nil {
		s.responseHistories = map[string]qoderResponseHistoryEntry{}
	}
	if _, exists := s.responseHistories[key]; exists {
		return
	}
	for len(s.responseHistories) >= qoderResponseHistoryLimit || s.responseHistoryBytes+len(payload) > qoderResponseHistoryTotalBytes {
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range s.responseHistories {
			if oldestKey == "" || entry.ExpiresAt.Before(oldest) {
				oldestKey, oldest = candidate, entry.ExpiresAt
			}
		}
		if oldestKey == "" {
			return
		}
		s.responseHistoryBytes -= len(s.responseHistories[oldestKey].Payload)
		delete(s.responseHistories, oldestKey)
	}
	ttl := s.Ttl
	if ttl <= 0 {
		ttl = QoderConversationTTL
	}
	s.responseHistories[key] = qoderResponseHistoryEntry{Payload: payload, ExpiresAt: now.Add(ttl)}
	s.responseHistoryBytes += len(payload)
}

// pruneResponseHistoriesLocked removes expired contexts and their byte charges.
// Callers hold the conversation mutex throughout traversal and accounting, so
// concurrent publication cannot lose or double-count a retained entry.
func (s *QoderConversationStore) pruneResponseHistoriesLocked(now time.Time) {
	for key, entry := range s.responseHistories {
		if !now.Before(entry.ExpiresAt) {
			s.responseHistoryBytes -= len(entry.Payload)
			delete(s.responseHistories, key)
		}
	}
}

// qoderResponseHistoryKey isolates replay data by the authenticated gateway key.
func qoderResponseHistoryKey(metadata RequestMetadata, responseID string) string {
	return QoderConversationExplicitSessionKey(metadata, responseID)
}
