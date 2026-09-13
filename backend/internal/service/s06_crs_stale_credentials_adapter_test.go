//go:build unit

package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"reflect"
)

// 新条件存储替身复用原测试的实际行，旧 HEAD 测试正文无需知道新契约。
func (r *crsStaleCredentialRepo) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, v account.CredentialVersion, credentials map[string]any) (bool, error) {
	c := r.current
	if c.ID != v.ID || c.Platform != v.Platform || c.Type != v.Type || c.Status != v.Status || !reflect.DeepEqual(c.Credentials, v.Credentials) || !reflect.DeepEqual(c.ProxyID, v.ProxyID) {
		return false, nil
	}
	return true, r.UpdateCredentials(ctx, v.ID, credentials)
}
