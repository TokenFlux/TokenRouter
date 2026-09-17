// ManagedKeys 供应服务端隐藏 Key，复用原创建路径与并发冲突后的单次回读。
package apikey

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type ManagedKeyStore interface {
	GetManagedKeyByUserAndGroup(context.Context, int64, int64, string) (*APIKey, error)
	CreateManagedKey(context.Context, *APIKey) error
}
type ManagedKeys struct {
	Store      ManagedKeyStore
	Prefix     string
	ManagedBy  string
	NamePrefix string
}

func (s ManagedKeys) Ensure(ctx context.Context, userID, groupID int64) (*APIKey, error) {
	if s.Store == nil {
		return nil, errors.New("creative managed key repository is not configured")
	}
	existing, err := s.Store.GetManagedKeyByUserAndGroup(ctx, userID, groupID, s.ManagedBy)
	if err == nil && existing != nil {
		return existing, nil
	}
	if err != nil && !errors.Is(err, ErrAPIKeyNotFound) {
		return nil, err
	}
	prefix := strings.TrimSpace(s.Prefix)
	if prefix == "" {
		prefix = "sk-"
	}
	keyString, err := GenerateAPIKeyString(prefix)
	if err != nil {
		return nil, err
	}
	managedBy := s.ManagedBy
	key := &APIKey{
		UserID:                                userID,
		Key:                                   keyString,
		Name:                                  fmt.Sprintf("%s:%d", s.NamePrefix, groupID),
		GroupID:                               &groupID,
		Status:                                StatusActive,
		BillingMode:                           APIKeyBillingModeAuto,
		ManagedBy:                             &managedBy,
		FastModePolicy:                        "follow_request",
		FallbackToDefaultGroupWhenUnavailable: false,
	}
	if err := s.Store.CreateManagedKey(ctx, key); err != nil {
		// 并发创建冲突：重查一次即可拿到已存在的 Key（创建幂等）。
		if existing, retryErr := s.Store.GetManagedKeyByUserAndGroup(ctx, userID, groupID, s.ManagedBy); retryErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return key, nil
}
