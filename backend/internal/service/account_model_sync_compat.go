// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
)

// AccountModelSyncFetch 仅转交原供应商请求/响应解析，S09 改绑。
func AccountModelSyncFetch(source *AccountTestService) func(context.Context, *account.Record) ([]string, error) {
	if source == nil {
		return nil
	}
	return func(ctx context.Context, v *account.Record) ([]string, error) {
		return source.FetchUpstreamSupportedModels(ctx, AccountFromRecord(v))
	}
}
