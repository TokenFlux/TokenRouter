package session

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// CompatResponses 保存兼容会话的响应归属、回合状态与禁用标记，三者共用原过期窗口。
type CompatResponses struct {
	bindings sync.Map
	TTL      func() time.Duration
}

func (s *CompatResponses) ttl() time.Duration {
	if s != nil && s.TTL != nil {
		return s.TTL()
	}
	return time.Hour
}

// CompatResponseKey 保留原账号、Key 与提示缓存的隔离编码。
func CompatResponseKey(accountID, apiKeyID int64, prompt string) string {
	key := strings.TrimSpace(prompt)
	if key == "" {
		return ""
	}
	return strings.Join([]string{strconv.FormatInt(accountID, 10), strconv.FormatInt(apiKeyID, 10), key}, "\x00")
}

type compatResponseBinding struct {
	ResponseID           string
	TurnState            string
	ContinuationDisabled bool
	ExpiresAt            time.Time
}

func (s *CompatResponses) Response(key string) string {
	if s == nil {
		return ""
	}
	if key == "" {
		return ""
	}
	raw, ok := s.bindings.Load(key)
	if !ok {
		return ""
	}
	binding, ok := raw.(compatResponseBinding)
	if !ok {
		s.bindings.Delete(key)
		return ""
	}
	if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
		s.bindings.Delete(key)
		return ""
	}
	if binding.ContinuationDisabled {
		return ""
	}
	if strings.TrimSpace(binding.ResponseID) == "" {
		s.bindings.Delete(key)
		return ""
	}
	return strings.TrimSpace(binding.ResponseID)
}
func (s *CompatResponses) BindResponse(key, responseID string) {
	if s == nil {
		return
	}
	id := strings.TrimSpace(responseID)
	if key == "" || id == "" {
		return
	}
	binding := compatResponseBinding{
		ResponseID: id,
		ExpiresAt:  time.Now().Add(s.ttl()),
	}
	if raw, ok := s.bindings.Load(key); ok {
		if existing, ok := raw.(compatResponseBinding); ok {
			if existing.ContinuationDisabled {
				existing.ResponseID = ""
				existing.ExpiresAt = time.Now().Add(s.ttl())
				s.bindings.Store(key, existing)
				return
			}
			binding.TurnState = existing.TurnState
		}
	}
	s.bindings.Store(key, binding)
}
func (s *CompatResponses) DeleteResponse(key string) {
	if s == nil {
		return
	}
	if key == "" {
		return
	}
	raw, ok := s.bindings.Load(key)
	if !ok {
		return
	}
	binding, ok := raw.(compatResponseBinding)
	if !ok {
		s.bindings.Delete(key)
		return
	}
	binding.ResponseID = ""
	if strings.TrimSpace(binding.TurnState) == "" && !binding.ContinuationDisabled {
		s.bindings.Delete(key)
		return
	}
	binding.ExpiresAt = time.Now().Add(s.ttl())
	s.bindings.Store(key, binding)
}
func (s *CompatResponses) Disable(key string) {
	if s == nil {
		return
	}
	if key == "" {
		return
	}
	binding := compatResponseBinding{
		ContinuationDisabled: true,
		ExpiresAt:            time.Now().Add(s.ttl()),
	}
	if raw, ok := s.bindings.Load(key); ok {
		if existing, ok := raw.(compatResponseBinding); ok {
			binding.TurnState = existing.TurnState
		}
	}
	s.bindings.Store(key, binding)
}
func (s *CompatResponses) Disabled(key string) bool {
	if s == nil {
		return false
	}
	if key == "" {
		return false
	}
	raw, ok := s.bindings.Load(key)
	if !ok {
		return false
	}
	binding, ok := raw.(compatResponseBinding)
	if !ok {
		s.bindings.Delete(key)
		return false
	}
	if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
		s.bindings.Delete(key)
		return false
	}
	return binding.ContinuationDisabled
}
func (s *CompatResponses) TurnState(key string) string {
	if s == nil {
		return ""
	}
	if key == "" {
		return ""
	}
	raw, ok := s.bindings.Load(key)
	if !ok {
		return ""
	}
	binding, ok := raw.(compatResponseBinding)
	if !ok || strings.TrimSpace(binding.TurnState) == "" {
		return ""
	}
	if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
		s.bindings.Delete(key)
		return ""
	}
	return strings.TrimSpace(binding.TurnState)
}
func (s *CompatResponses) BindTurnState(key, turnState string) {
	if s == nil {
		return
	}
	state := strings.TrimSpace(turnState)
	if key == "" || state == "" {
		return
	}
	binding := compatResponseBinding{
		TurnState: state,
		ExpiresAt: time.Now().Add(s.ttl()),
	}
	if raw, ok := s.bindings.Load(key); ok {
		if existing, ok := raw.(compatResponseBinding); ok {
			binding.ResponseID = existing.ResponseID
			binding.ContinuationDisabled = existing.ContinuationDisabled
		}
	}
	s.bindings.Store(key, binding)
}
