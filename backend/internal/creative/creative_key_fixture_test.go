//go:build unit

// 托管 Key 的旧形状仅在兼容边界转换。
package creative_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

type creativeManagedKeys struct{ source creativeFixtureKeys }

func (s creativeManagedKeys) GetManagedKeyByUserAndGroup(ctx context.Context, u, g int64, owner string) (*apikey.APIKey, error) {
	v, err := s.source.GetManagedKeyByUserAndGroup(ctx, u, g, owner)
	return apikey.CopyAPIKey(v), err
}
func (s creativeManagedKeys) CreateManagedKey(ctx context.Context, k *apikey.APIKey) error {
	v := apikey.CopyAPIKey(k)
	err := s.source.CreateManagedKey(ctx, v)

	*k = *apikey.CopyAPIKey(v)
	return err
}
