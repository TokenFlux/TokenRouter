// 旧网关只投影会话隔离输入，裁决由 gateway/session 唯一实现。
package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

const SessionIsolationSourceOpenAI = session.SessionIsolationSourceOpenAI
const SessionIsolationSourceOpenAIPreviousResponse = session.SessionIsolationSourceOpenAIPreviousResponse
const SessionIsolationSourceGateway = session.SessionIsolationSourceGateway
const SessionIsolationSourceGemini = session.SessionIsolationSourceGemini
const SessionIsolationConflictMessage = session.SessionIsolationConflictMessage

var ErrSessionIsolationConflict = session.ErrSessionIsolationConflict

// EnsureSessionIsolation 记录显式会话 owner，并在目标分组开启隔离时拒绝跨分组切入。
func (s *GatewayService) EnsureSessionIsolation(ctx context.Context, apiKey *APIKey, userID int64, source, sessionHash string) error {
	return ensureSessionIsolation(ctx, s.cache, apiKey, userID, source, sessionHash, stickySessionTTL)
}

// EnsureSessionIsolation 记录 OpenAI 显式会话 owner，并在目标分组开启隔离时拒绝跨分组切入。
func (s *OpenAIGatewayService) EnsureSessionIsolation(ctx context.Context, apiKey *APIKey, userID int64, source, sessionHash string) error {
	return ensureSessionIsolation(ctx, s.cache, apiKey, userID, source, sessionHash, openaiStickySessionTTL)
}
func ensureSessionIsolation(ctx context.Context, cache GatewayCache, key *APIKey, userID int64, source, hash string, ttl time.Duration) error {
	if key == nil {
		return nil
	}
	return session.EnsureIsolation(ctx, cache, session.IsolationInput{UserID: userID, GroupID: derefGroupID(key.GroupID), Source: source, Hash: hash, TTL: ttl, Enabled: key.Group != nil && key.Group.SessionIsolationEnabled})
}
