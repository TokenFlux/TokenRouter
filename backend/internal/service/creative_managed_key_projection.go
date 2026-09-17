// 托管 Key 的旧形状仅在兼容边界转换。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

type creativeManagedKeys struct{ CreativeManagedKeyRepository }

func (s creativeManagedKeys) GetManagedKeyByUserAndGroup(ctx context.Context, u, g int64, owner string) (*apikey.APIKey, error) {
	v, err := s.CreativeManagedKeyRepository.GetManagedKeyByUserAndGroup(ctx, u, g, owner)
	return APIKeyView(v), err
}
func (s creativeManagedKeys) CreateManagedKey(ctx context.Context, k *apikey.APIKey) error {
	v := APIKeyFromView(k)
	err := s.CreativeManagedKeyRepository.CreateManagedKey(ctx, v)

	*k = *APIKeyView(v)
	return err
}
