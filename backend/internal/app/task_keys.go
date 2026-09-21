package app

import (
	"context"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
)

// creativeManagedKeys 为尚未清零的任务消费者投影唯一 KeyStore，不另建存储实例。
type creativeManagedKeys struct {
	store *keypostgres.KeyStore
}

func (keys creativeManagedKeys) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*apikey.APIKey, error) {
	key, err := keys.store.GetManagedKeyByUserAndGroup(ctx, userID, groupID, managedBy)
	return apikey.CopyAPIKey(key), err
}

func (keys creativeManagedKeys) CreateManagedKey(ctx context.Context, key *apikey.APIKey) error {
	view := apikey.CopyAPIKey(key)
	err := keys.store.CreateManagedKey(ctx, view)
	if key != nil && view != nil {
		*key = *apikey.CopyAPIKey(view)
	}
	return err
}
