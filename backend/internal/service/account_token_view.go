package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// accountTokenSource 仅供尚未迁完的执行账号视图过渡，原生源不接收旧实体。
type accountTokenSource interface {
	GetAccessToken(context.Context, *account.Record) (string, error)
}

// accountToken 保留 Gemini/Antigravity project 回填后的旧调用方赋值时机。
func accountToken(ctx context.Context, source accountTokenSource, value *Account) (string, error) {
	record := AccountRecordView(value)
	token, err := source.GetAccessToken(ctx, record)
	if value != nil && record != nil {
		value.Credentials = record.Credentials
	}
	return token, err
}

func GeminiTokenCacheKey(value *Account) string {
	return provider.GeminiTokenCacheKey(AccountRecordView(value))
}

func AntigravityTokenCacheKey(value *Account) string {
	return account.AntigravityTokenCacheKey(AccountRecordView(value))
}
