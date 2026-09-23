//go:build unit

package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// stubCredRepo 是最小化 AccountRepository stub，仅实现 GetByID，供 credential_shadow_test 使用。
// 嵌入接口满足完整方法集；未实现的方法若被调用会 panic，从而快速暴露误调用。
type stubCredRepo struct {
	gatewayprovider.ExecutionAccountStore

	parent *gatewayprovider.ExecutionAccount
}

func (s *stubCredRepo) GetByID(_ context.Context, _ int64) (*gatewayprovider.ExecutionAccount, error) {
	return s.parent, nil
}

func newStubCredRepo(parent *gatewayprovider.ExecutionAccount) gatewayprovider.ExecutionAccountStore {
	return &stubCredRepo{parent: parent}
}
