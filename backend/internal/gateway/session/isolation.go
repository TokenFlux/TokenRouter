package session

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// OwnerStore 沿用已有原子首次归属与 TTL 接口。
type OwnerStore interface {
	SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error)
	GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error)
	RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error
}

// IsolationInput 不携带完整 Key 或分组实体。
type IsolationInput struct {
	UserID, GroupID int64
	Source, Hash    string
	Enabled         bool
	TTL             time.Duration
}

const (
	SessionIsolationSourceOpenAI                 = "openai"
	SessionIsolationSourceOpenAIPreviousResponse = "openai_previous_response"
	SessionIsolationSourceGateway                = "gateway"
	SessionIsolationSourceGemini                 = "gemini"

	SessionIsolationConflictMessage = "This session already belongs to another group and cannot switch to the current session-isolated group"
)

var ErrSessionIsolationConflict = infraerrors.Forbidden("SESSION_ISOLATION_CONFLICT", SessionIsolationConflictMessage)

// EnsureIsolation 只处理已投影的用户、最终分组与会话标识。
func EnsureIsolation(ctx context.Context, cache OwnerStore, input IsolationInput) error {
	userID, source, sessionHash, ttl := input.UserID, input.Source, input.Hash, input.TTL
	source = strings.TrimSpace(source)
	sessionHash = strings.TrimSpace(sessionHash)
	if cache == nil || userID <= 0 || source == "" || sessionHash == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	targetGroupID := input.GroupID
	targetIsolationEnabled := input.Enabled

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
