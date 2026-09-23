package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// HTTP 集成夹具复用所传存储和 token 源，不创建第二个缓存或刷新器。
func newOpenAIExecutionCredentialsForTest(repo gatewayprovider.ExecutionAccountStore, grok *account.GrokTokenSource) *account.OpenAIExecutionCredentials {
	out := &account.OpenAIExecutionCredentials{}
	if repo != nil {
		out.Parent = func(ctx context.Context, id int64) (*account.Record, error) {
			value, err := repo.GetByID(ctx, id)
			return gatewayprovider.ExecutionRecord(value), err
		}
	}
	if grok != nil {
		out.Grok = grok.GetAccessToken
	}
	return out
}
