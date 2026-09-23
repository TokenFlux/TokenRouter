package session

import (
	"context"
	"time"
)

// CyberLookup 只携带本次查询；显式键惰性解析，关闭功能或无存储时不读取报文。
type CyberLookup struct {
	APIKeyID            int64
	Body                []byte
	ClientIP, UserAgent string
	ExplicitKey         func() string
}

// CyberBlocks 只编排已有会话存储，动态开关在原调用位置读取。
type CyberBlocks struct {
	store    CyberSessionBlockStore
	settings func(context.Context) (bool, time.Duration)
	log      func(string, ...any)
}

func NewCyberBlocks(store CyberSessionBlockStore, settings func(context.Context) (bool, time.Duration), log func(string, ...any)) *CyberBlocks {
	return &CyberBlocks{store: store, settings: settings, log: log}
}
func (s *CyberBlocks) Runtime(ctx context.Context) (bool, time.Duration) {
	if s == nil || s.settings == nil {
		return false, time.Hour
	}
	return s.settings(ctx)
}
func (s *CyberBlocks) logf(format string, args ...any) {
	if s != nil && s.log != nil {
		s.log(format, args...)
	}
}
func (s *CyberBlocks) MarkCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	enabled, ttl := s.Runtime(ctx)
	if !enabled {
		return
	}
	store := s.store
	if store == nil {
		return
	}
	if err := store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl); err != nil {
		s.logf("cyber session block write failed: err=%v", err)
	}
}

func (s *CyberBlocks) Find(ctx context.Context, input CyberLookup) string {
	apiKeyID, body, clientIP, userAgent := input.APIKeyID, input.Body, input.ClientIP, input.UserAgent
	enabled, _ := s.Runtime(ctx)
	if !enabled {
		return ""
	}
	store := s.store
	if store == nil {
		return ""
	}
	explicitKey := ""
	if input.ExplicitKey != nil {
		explicitKey = input.ExplicitKey()
	}
	if explicitKey != "" {
		key, err := store.FindCyberSessionBlocked(ctx, []string{explicitKey})
		if err != nil {
			s.logf("cyber explicit session read failed: err=%v", err)
			return ""
		}
		if key != "" {
			return key
		}
	}
	scopeKey := CyberSessionScopeKey(apiKeyID, clientIP, userAgent)
	active, err := store.IsCyberSessionScopeActive(ctx, scopeKey)
	if err != nil {
		s.logf("cyber session scope read failed: err=%v", err)
		return ""
	}
	if !active {
		return ""
	}
	transcript := deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body)
	if transcript.lookupKeysTruncated {
		// scope 已激活时不能静默丢弃旧候选，否则追加无关条目可绕过前缀匹配。
		return cyberSessionTranscriptLookupOverflowBlockKey
	}
	keys := transcript.lookupKeys
	if len(keys) == 0 {
		return ""
	}
	key, err := store.FindCyberSessionBlocked(ctx, keys)
	if err != nil {
		s.logf("cyber session block batch read failed: err=%v", err)
		return ""
	}
	return key
}
