package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ExecutionTokenSource 仅供执行账号的受控凭据读取，原生源不接收旧实体。
type ExecutionTokenSource interface {
	GetAccessToken(context.Context, *account.Record) (string, error)
}

// ExecutionToken 保留 Gemini/Antigravity project 回填后的旧调用方赋值时机。
func ExecutionToken(ctx context.Context, source ExecutionTokenSource, value *ExecutionAccount) (string, error) {
	record := ExecutionRecord(value)
	token, err := source.GetAccessToken(ctx, record)
	if value != nil && record != nil {
		value.Record.Credentials = record.Credentials
	}
	return token, err
}
