package httpapi

import (
	"context"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 创建契约仅记录提交给用例的输入，其它能力不提供默认业务实现。
type managementCreateFixture struct {
	AccountManagement
	mu               sync.Mutex
	createdAccounts  []*account.CreateAccountInput
	createAccountErr error
}

func (s *managementCreateFixture) CreateAccount(_ context.Context, input *account.CreateAccountInput) (*account.Record, error) {
	s.mu.Lock()
	s.createdAccounts = append(s.createdAccounts, input)
	s.mu.Unlock()
	if s.createAccountErr != nil {
		return nil, s.createAccountErr
	}
	return &account.Record{ID: 300, Name: input.Name, Status: account.StatusActive}, nil
}
func (s *managementCreateFixture) ForceOpenAIPrivacy(context.Context, *account.Record) string {
	return ""
}
func (s *managementCreateFixture) ForceAntigravityPrivacy(context.Context, *account.Record) string {
	return ""
}

// 测试使用同一原生 HTTP、批处理和展示实现，不构造旧管理员聚合。
func newManagementCreateFixtureHandler(source *managementCreateFixture) *ManagementHandler {
	presenter := NewRuntimePresenter(account.NewRuntimeStatusReader(account.RuntimeStatusOptions{}), source, nil)
	batch := account.NewManagementBatch(source, nil, account.ManagementCreationOptions{Privacy: source})
	return NewManagementHandler(source, ManagementOptions{Presenter: presenter, Privacy: source, Batch: batch})
}
