// 凭据目标适配只投影母账号查询；影子资格规则由 account 唯一实现。
package provider

import (
	"context"
	"net/http"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func CredentialAccount(ctx context.Context, repo ExecutionAccountReader, value *ExecutionAccount) (*ExecutionAccount, error) {
	input := ExecutionRecord(value)
	var parent *ExecutionAccount
	resolved, err := accountcore.ResolveCredentialRecord(ctx, func(ctx context.Context, id int64) (*accountcore.Record, error) {
		var readErr error
		parent, readErr = repo.GetByID(ctx, id)
		return ExecutionRecord(parent), readErr
	}, input)
	if err != nil {
		return nil, err
	}
	if resolved == input {
		return value, nil
	}
	return parent, nil
}

// ExecutionAccountReader 只开放此处所需的一次账号读取。
type ExecutionAccountReader interface {
	GetByID(context.Context, int64) (*ExecutionAccount, error)
}

// CredentialChatGPTHeaders 保留先解析母账号、再应用请求头的原顺序。
func CredentialChatGPTHeaders(ctx context.Context, reader ExecutionAccountReader, headers http.Header, value *ExecutionAccount) error {
	resolved, err := CredentialAccount(ctx, reader, value)
	if err != nil {
		return err
	}
	accountprovider.SetChatGPTAccountHeaders(headers, resolved.View())
	return nil
}
