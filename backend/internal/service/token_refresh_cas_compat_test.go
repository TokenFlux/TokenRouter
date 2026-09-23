//go:build unit

package service

import (
	"context"
	"reflect"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 旧后台用例替身补入实际条件写契约，保留原写入计数、失败注入和提交后取消断言。
func (r *tokenRefreshAccountRepo) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version account.CredentialVersion, credentials map[string]any) (bool, error) {
	current := r.accountsByID[version.ID]
	if current == nil {
		return false, nil
	}
	expected := current.Record.Credentials
	if expected == nil {
		expected = map[string]any{}
	}
	if current.Record.ID != version.ID || current.Record.Platform != version.Platform || current.Record.Type != version.Type || current.Record.Status != version.Status || !reflect.DeepEqual(current.Record.ProxyID, version.ProxyID) || !reflect.DeepEqual(expected, version.Credentials) {
		return false, nil
	}
	err := r.UpdateCredentials(ctx, version.ID, credentials)
	return err == nil, err
}
