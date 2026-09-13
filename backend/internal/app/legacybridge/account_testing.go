// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountTestLoader 只转交受控执行句柄，不读取或缓存凭据。
type AccountTestLoader struct{ Source *service.AccountTestService }

func (s AccountTestLoader) LoadTestTarget(ctx context.Context, request account.TestRequest) (account.TestTarget, error) {
	return s.Source.LoadTestTarget(ctx, request)
}
