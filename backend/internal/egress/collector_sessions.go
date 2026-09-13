// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	rand "crypto/rand"
	hex "encoding/hex"
	errors "errors"
	sort "sort"
	sync "sync"
	time "time"
)

// CaptureSessions 拥有短期会话、到期和记录上限，不能访问监听或证书文件。
type CaptureSessions struct {
	mu     sync.Mutex
	states map[string]*captureSession
	now    func() time.Time
}
type captureSession struct {
	token     string
	expiresAt time.Time
	records   []*TLSFingerprintCaptureRecord
}

func NewCaptureSessions(now func() time.Time) *CaptureSessions {
	if now == nil {
		now = time.Now
	}
	return &CaptureSessions{states: make(map[string]*captureSession), now: now}
}
func (s *CaptureSessions) prune(now time.Time) {
	for token, state := range s.states {
		if now.After(state.expiresAt) {
			delete(s.states, token)
		}
	}
}
func (s *CaptureSessions) state(token string) (*captureSession, error) {
	s.prune(s.now())
	state := s.states[token]
	if state == nil {
		return nil, errors.New("capture session not found or expired")
	}
	return state, nil
}
func (s *CaptureSessions) Create(baseURL, caPEM string, ttl time.Duration) (*TLSFingerprintCollectorSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.now())
	token, err := randomHexToken(24)
	if err != nil {
		return nil, err
	}
	expires := s.now().Add(ttl)
	s.states[token] = &captureSession{token: token, expiresAt: expires}
	return &TLSFingerprintCollectorSession{Token: token, ExpiresAt: expires, CaptureURL: baseURL + "/capture/" + token, CAPEM: caPEM}, nil
}
func (s *CaptureSessions) Append(token string, record *TLSFingerprintCaptureRecord, max int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.state(token)
	if err != nil {
		return err
	}
	state.records = append([]*TLSFingerprintCaptureRecord{record}, state.records...)
	if len(state.records) > max {
		state.records = state.records[:max]
	}
	return nil
}
func (s *CaptureSessions) List(token string) ([]*TLSFingerprintCaptureRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.state(token)
	if err != nil {
		return nil, err
	}
	out := make([]*TLSFingerprintCaptureRecord, len(state.records))
	copy(out, state.records)
	sort.Slice(out, func(i, j int) bool { return out[i].CapturedAt.After(out[j].CapturedAt) })
	return out, nil
}
func (s *CaptureSessions) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, token)
}
func randomHexToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
