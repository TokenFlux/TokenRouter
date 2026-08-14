package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
)

const (
	SessionIsolationSourceOpenAI                 = "openai"
	SessionIsolationSourceOpenAIPreviousResponse = "openai_previous_response"
	SessionIsolationSourceGateway                = "gateway"
	SessionIsolationSourceGemini                 = "gemini"

	SessionIsolationConflictMessage = "This session already belongs to another group and cannot switch to the current session-isolated group"
)

var ErrSessionIsolationConflict = infraerrors.Forbidden("SESSION_ISOLATION_CONFLICT", SessionIsolationConflictMessage)

// EnsureSessionIsolation 记录显式会话 owner，并在目标分组开启隔离时拒绝跨分组切入。
func (s *GatewayService) EnsureSessionIsolation(ctx context.Context, apiKey *APIKey, userID int64, source, sessionHash string) error {
	return ensureSessionIsolation(ctx, s.cache, apiKey, userID, source, sessionHash, stickySessionTTL)
}

// EnsureSessionIsolation 记录 OpenAI 显式会话 owner，并在目标分组开启隔离时拒绝跨分组切入。
func (s *OpenAIGatewayService) EnsureSessionIsolation(ctx context.Context, apiKey *APIKey, userID int64, source, sessionHash string) error {
	return ensureSessionIsolation(ctx, s.cache, apiKey, userID, source, sessionHash, openaiStickySessionTTL)
}

func ensureSessionIsolation(ctx context.Context, cache GatewayCache, apiKey *APIKey, userID int64, source, sessionHash string, ttl time.Duration) error {
	source = strings.TrimSpace(source)
	sessionHash = strings.TrimSpace(sessionHash)
	if cache == nil || apiKey == nil || userID <= 0 || source == "" || sessionHash == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	targetGroupID := derefGroupID(apiKey.GroupID)
	targetIsolationEnabled := apiKey.Group != nil && apiKey.Group.SessionIsolationEnabled

	// 所有显式会话都会先尝试绑定首次归属；未开启隔离的分组也会成为 owner。
	written, err := cache.SetSessionOwnerGroupID(ctx, userID, source, sessionHash, targetGroupID, ttl)
	if err != nil {
		return err
	}
	if written {
		return nil
	}

	ownerGroupID, err := cache.GetSessionOwnerGroupID(ctx, userID, source, sessionHash)
	if err != nil {
		// owner 在 SetNX 与 Get 之间过期时，允许再尝试一次首次绑定。
		written, setErr := cache.SetSessionOwnerGroupID(ctx, userID, source, sessionHash, targetGroupID, ttl)
		if setErr != nil {
			return setErr
		}
		if written {
			return nil
		}
		ownerGroupID, err = cache.GetSessionOwnerGroupID(ctx, userID, source, sessionHash)
		if err != nil {
			return err
		}
	}

	if ownerGroupID == targetGroupID {
		return cache.RefreshSessionOwnerTTL(ctx, userID, source, sessionHash, ttl)
	}
	if !targetIsolationEnabled {
		return nil
	}
	return ErrSessionIsolationConflict
}
