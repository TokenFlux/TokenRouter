package session

import (
	"sync"
	"sync/atomic"
	"time"
)

// CodexTurnOrigins 只记录不透明回合状态的签发账号，不保存状态值或凭据。
// HTTP 暂存头真正提交后才登记，账号切换时据此判断是否移除回带值。
type CodexTurnOrigins struct {
	origins sync.Map
	writes  atomic.Uint64
	now     func() time.Time
}
type codexTurnOrigin struct {
	accountID int64
	expiresAt time.Time
}

func NewCodexTurnOrigins(now func() time.Time) *CodexTurnOrigins {
	if now == nil {
		now = time.Now
	}
	return &CodexTurnOrigins{now: now}
}

// Record 沿用写入时的 TTL，每 256 次登记清扫一次过期记录。
func (s *CodexTurnOrigins) Record(seed string, id int64, ttl time.Duration) {
	if s == nil || seed == "" || id <= 0 {
		return
	}
	s.origins.Store(seed, codexTurnOrigin{accountID: id, expiresAt: s.now().Add(ttl)})
	if s.writes.Add(1)%256 != 0 {
		return
	}
	now := s.now()
	s.origins.Range(func(key, value any) bool {
		origin, ok := value.(codexTurnOrigin)
		if !ok || (!origin.expiresAt.IsZero() && now.After(origin.expiresAt)) {
			s.origins.Delete(key)
		}
		return true
	})
}

// Owner 仅返回仍有效的签发账号；未知或已过期的来源保持原透传资格。
func (s *CodexTurnOrigins) Owner(seed string) (int64, bool) {
	if s == nil || seed == "" {
		return 0, false
	}
	value, ok := s.origins.Load(seed)
	if !ok {
		return 0, false
	}
	origin, ok := value.(codexTurnOrigin)
	if !ok || (!origin.expiresAt.IsZero() && s.now().After(origin.expiresAt)) {
		s.origins.Delete(seed)
		return 0, false
	}
	return origin.accountID, true
}
