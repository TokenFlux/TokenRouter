//go:build unit

// 生产消费者已迁出；原 unit 断言通过唯一实现的兼容入口验证。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const (
	grokObservedModelsExtraKey = account.GrokObservedModelsExtraKey
	grokObservedModelsTTL      = account.GrokObservedModelsTTL
	grokObservedModelsTimeout  = account.GrokObservedModelsTimeout
)

func extractGrokModelIDsFromModelsBody(body []byte) []string { return xai.ExtractModelIDs(body) }
func (s *GrokQuotaService) syncGrokObservedModels(ctx context.Context, acc *Account) error {
	if s == nil {
		return nil
	}
	return account.SyncGrokObservedModels(ctx, s.grokModelsOptions(), AccountRecordView(acc))
}
