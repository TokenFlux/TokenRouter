package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// QuotaFetcher 额度获取接口，各平台实现此接口
type QuotaFetcher interface {
	// CanFetch 检查是否可以获取此账户的额度
	CanFetch(account *Account) bool
	// FetchQuota 获取账户额度信息
	FetchQuota(ctx context.Context, account *Account, proxyURL string) (*QuotaResult, error)
}

type QuotaResult = account.QuotaResult
