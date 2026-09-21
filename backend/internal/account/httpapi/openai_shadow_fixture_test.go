//go:build unit

package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func (s *openAIShadowFixture) CreateShadow(ctx context.Context, parentID int64, opts accountcore.ShadowOptions) (*accountcore.Record, error) {
	if s.createSparkShadowErr != nil {
		return nil, s.createSparkShadowErr
	}
	pid := parentID
	return &accountcore.Record{
		ID:              9001,
		Name:            opts.Name,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Priority:        opts.Priority,
		Concurrency:     opts.Concurrency,
		GroupIDs:        opts.GroupIDs,
		ParentAccountID: &pid,
		QuotaDimension:  accountcore.QuotaDimensionSpark,
		Credentials:     map[string]any{},
		Extra:           map[string]any{},
	}, nil
}

// openAIShadowFixture 只保留原影子创建输出与受控失败。
type openAIShadowFixture struct {
	OpenAIAdminOperations
	createSparkShadowErr error
}
