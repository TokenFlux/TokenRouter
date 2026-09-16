// 旧凭据入口只投影母账号查询；影子资格规则由 account 唯一实现。
package service

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func resolveCredentialAccount(ctx context.Context, repo AccountRepository, value *Account) (*Account, error) {
	input := AccountRecordView(value)
	var parent *Account
	resolved, err := accountcore.ResolveCredentialRecord(ctx, func(ctx context.Context, id int64) (*accountcore.Record, error) {
		var readErr error
		parent, readErr = repo.GetByID(ctx, id)
		return AccountRecordView(parent), readErr
	}, input)
	if err != nil {
		return nil, err
	}
	if resolved == input {
		return value, nil
	}
	return parent, nil
}
