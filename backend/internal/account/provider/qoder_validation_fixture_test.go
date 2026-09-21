// 本文件只为账号创建和编辑契约保存原生记录，不复制管理规则。
package provider

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/google/uuid"
)

type qoderValidationStore struct {
	account.AdminStore
	accounts map[int64]*account.Record
}

func (s *qoderValidationStore) Create(_ context.Context, value *account.Record) error {
	if s.accounts == nil {
		s.accounts = make(map[int64]*account.Record)
	}
	if value.ID == 0 {
		value.ID = int64(len(s.accounts) + 1)
	}
	s.accounts[value.ID] = account.CloneRecord(value)
	return nil
}

func (s *qoderValidationStore) GetByID(_ context.Context, id int64) (*account.Record, error) {
	return account.CloneRecord(s.accounts[id]), nil
}

func (s *qoderValidationStore) Update(_ context.Context, value *account.Record) error {
	s.accounts[value.ID] = account.CloneRecord(value)
	return nil
}

func newQoderValidationAdmin(store *qoderValidationStore, validator *qoderCredentialValidator) *account.Admin {
	return account.NewAdmin(store, account.AdminOptions{
		Creation:    account.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString},
		Credentials: validator.hooks(),
	})
}
