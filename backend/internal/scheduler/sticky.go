package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
)

// StickyCache 只持有调度粘性与显式会话分组归属，不包含登录或上游远端会话。
type StickyCache interface {
	GetSessionAccountID(context.Context, int64, string) (int64, error)
	SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error
	RefreshSessionTTL(context.Context, int64, string, time.Duration) error
	DeleteSessionAccountID(context.Context, int64, string) error
}
type SessionOwnerCache interface {
	SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error)
	GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error)
	RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error
}

// StickyStats 仅有一个进程实例，各次兼容入口复用此观测。
type StickyStats struct {
	readFallbackTotal atomic.Int64
	readFallbackHit   atomic.Int64
	dualWriteTotal    atomic.Int64
}

func (s *StickyStats) Snapshot() (int64, int64, int64) {
	return s.readFallbackTotal.Load(), s.readFallbackHit.Load(), s.dualWriteTotal.Load()
}

type StickyOptions struct {
	Prefix          string
	ReadLegacy      bool
	DualWriteLegacy bool
	DefaultTTL      time.Duration
}

// StickySession 的配置由平台入口显式投影，算法不读取平台配置或 HTTP Context。
type StickySession struct {
	cache   StickyCache
	options StickyOptions
	stats   *StickyStats
}

func NewStickySession(cache StickyCache, options StickyOptions, stats *StickyStats) *StickySession {
	return &StickySession{cache: cache, options: options, stats: stats}
}
func (s *StickySession) SessionKey(hash string) string {
	v := strings.TrimSpace(hash)
	if v == "" {
		return ""
	}
	return s.options.Prefix + v
}
func (s *StickySession) LegacyKey(hash, legacy string) string {
	v := strings.TrimSpace(legacy)
	if v == "" {
		return ""
	}
	key := s.options.Prefix + v
	if key == s.SessionKey(hash) {
		return ""
	}
	return key
}
func (s *StickySession) LegacyTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = s.options.DefaultTTL
	}
	if ttl > 10*time.Minute {
		return 10 * time.Minute
	}
	return ttl
}
func DeriveSessionHashes(sessionID string) (currentHash string, legacyHash string) {
	normalized := strings.TrimSpace(sessionID)
	if normalized == "" {
		return "", ""
	}

	currentHash = fmt.Sprintf("%016x", xxhash.Sum64String(normalized))
	sum := sha256.Sum256([]byte(normalized))
	legacyHash = hex.EncodeToString(sum[:])
	return currentHash, legacyHash
}
func (s *StickySession) Get(ctx context.Context, groupID int64, sessionHash, legacyHash string) (int64, error) {
	if s == nil || s.cache == nil {
		return 0, nil
	}

	primaryKey := s.SessionKey(sessionHash)
	if primaryKey == "" {
		return 0, nil
	}

	accountID, err := s.cache.GetSessionAccountID(ctx, groupID, primaryKey)
	if err == nil && accountID > 0 {
		return accountID, nil
	}
	if !s.options.ReadLegacy {
		return accountID, err
	}

	legacyKey := s.LegacyKey(sessionHash, legacyHash)
	if legacyKey == "" {
		return accountID, err
	}

	s.stats.readFallbackTotal.Add(1)
	legacyAccountID, legacyErr := s.cache.GetSessionAccountID(ctx, groupID, legacyKey)
	if legacyErr == nil && legacyAccountID > 0 {
		s.stats.readFallbackHit.Add(1)
		return legacyAccountID, nil
	}
	return accountID, err
}

func (s *StickySession) Set(ctx context.Context, groupID int64, sessionHash, legacyHash string, accountID int64, ttl time.Duration) error {
	if s == nil || s.cache == nil || accountID <= 0 {
		return nil
	}
	primaryKey := s.SessionKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	if err := s.cache.SetSessionAccountID(ctx, groupID, primaryKey, accountID, ttl); err != nil {
		return err
	}

	if !s.options.DualWriteLegacy {
		return nil
	}
	legacyKey := s.LegacyKey(sessionHash, legacyHash)
	if legacyKey == "" {
		return nil
	}
	if err := s.cache.SetSessionAccountID(ctx, groupID, legacyKey, accountID, s.LegacyTTL(ttl)); err != nil {
		return err
	}
	s.stats.dualWriteTotal.Add(1)
	return nil
}

func (s *StickySession) Refresh(ctx context.Context, groupID int64, sessionHash, legacyHash string, ttl time.Duration) error {
	if s == nil || s.cache == nil {
		return nil
	}
	primaryKey := s.SessionKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	err := s.cache.RefreshSessionTTL(ctx, groupID, primaryKey, ttl)
	if !s.options.ReadLegacy && !s.options.DualWriteLegacy {
		return err
	}

	legacyKey := s.LegacyKey(sessionHash, legacyHash)
	if legacyKey != "" {
		_ = s.cache.RefreshSessionTTL(ctx, groupID, legacyKey, s.LegacyTTL(ttl))
	}
	return err
}

func (s *StickySession) Delete(ctx context.Context, groupID int64, sessionHash, legacyHash string) error {
	if s == nil || s.cache == nil {
		return nil
	}
	primaryKey := s.SessionKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	err := s.cache.DeleteSessionAccountID(ctx, groupID, primaryKey)
	if !s.options.ReadLegacy && !s.options.DualWriteLegacy {
		return err
	}

	legacyKey := s.LegacyKey(sessionHash, legacyHash)
	if legacyKey != "" {
		_ = s.cache.DeleteSessionAccountID(ctx, groupID, legacyKey)
	}
	return err
}
