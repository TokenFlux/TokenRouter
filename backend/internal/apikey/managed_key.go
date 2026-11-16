package apikey

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// managed_key.go provisions and reuses server-managed execution credentials by
// user, group, and manager identity. Provisioning creates an active
// automatic-funding Key when that lookup is missing, and a failed creation
// triggers one lookup retry to accept a credential that another caller may
// have created.
//
// The lookup, insertion, and recovery read can overlap another provisioner. A
// successful recovery read returns the stored credential even when this caller
// generated a different candidate secret. If that read fails or finds nothing,
// the creation error remains the result. Storage supplies the uniqueness and
// insertion behavior; this service chooses which result to return after those
// operations.

// ManagedKeyStore locates and creates server-provisioned credentials for a
// named manager.
type ManagedKeyStore interface {
	// GetManagedKeyByUserAndGroup loads the credential matching user, group and
	// manager. It returns ErrAPIKeyNotFound when missing so provisioning can
	// distinguish absence from a storage failure.
	GetManagedKeyByUserAndGroup(context.Context, int64, int64, string) (*APIKey, error)
	// CreateManagedKey persists the supplied managed record and fills its stored
	// identifiers and timestamps. Callers supply manager identity and treat
	// creation conflicts as an opportunity to reload the matching record.
	CreateManagedKey(context.Context, *APIKey) error
}

// ManagedKeys provisions reusable execution credentials with configured manager
// and naming values.
type ManagedKeys struct {
	Store      ManagedKeyStore // managed credential persistence dependency
	Prefix     string          // generated credential prefix text
	ManagedBy  string          // persisted provisioning manager identity
	NamePrefix string          // generated display name prefix
}

// Ensure returns an existing managed Key or creates one for userID and groupID.
// Missing storage reports a configuration error; a missing Key selects a random
// credential with the trimmed prefix or sk- by default. Creation failures
// trigger a second lookup, and a returned existing Key wins over the creation
// error. Returned records belong to the store or new creation, so this method
// supplies no independent copy.
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
		UserID:                       userID,
		Key:                          keyString,
		Name:                         fmt.Sprintf("%s:%d", s.NamePrefix, groupID),
		GroupID:                      &groupID,
		Status:                       StatusActive,
		BillingMode:                  APIKeyBillingModeAuto,
		ManagedBy:                    &managedBy,
		FastModePolicy:               "follow_request",
		ForceClaude1hCache:           false,
		FallbackWhenGroupUnavailable: false,
	}
	if err := s.Store.CreateManagedKey(ctx, key); err != nil {
		// A failed write may race with another successful provision. Reload by
		// manager identity and group so callers can use that stored execution Key.
		if existing, retryErr := s.Store.GetManagedKeyByUserAndGroup(ctx, userID, groupID, s.ManagedBy); retryErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return key, nil
}
