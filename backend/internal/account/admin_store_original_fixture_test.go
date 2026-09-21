package account_test

import (
	"context"
	"sync"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// accountServiceTestRepo 为账号服务测试提供最小内存仓储。
type accountServiceTestRepo struct {
	accountcore.AdminStore
	mu          sync.Mutex
	accounts    map[int64]*accountcore.Record
	updates     map[int64][]map[string]any
	bulkUpdates []accountcore.AccountBulkUpdate
}

func (r *accountServiceTestRepo) Create(_ context.Context, account *accountcore.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.accounts == nil {
		r.accounts = make(map[int64]*accountcore.Record)
	}
	if account.ID == 0 {
		account.ID = int64(len(r.accounts) + 1)
	}
	r.accounts[account.ID] = accountcore.CloneRecord(account)
	return nil
}

func (r *accountServiceTestRepo) Update(_ context.Context, account *accountcore.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.accounts == nil {
		r.accounts = make(map[int64]*accountcore.Record)
	}
	r.accounts[account.ID] = accountcore.CloneRecord(account)
	return nil
}

func (r *accountServiceTestRepo) BulkUpdate(_ context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bulkUpdates = append(r.bulkUpdates, updates)
	return int64(len(ids)), nil
}

func (r *accountServiceTestRepo) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return nil, accountcore.ErrAccountNotFound
	}
	clone := *account
	clone.Credentials = accountcore.CRSMergeMap(nil, account.Credentials)
	clone.Extra = accountcore.CRSMergeMap(nil, account.Extra)
	clone.LoadLocation = time.LoadLocation
	return &clone, nil
}

func (r *accountServiceTestRepo) GetByIDs(_ context.Context, ids []int64) ([]*accountcore.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*accountcore.Record, 0, len(ids))
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			result = append(result, account)
		}
	}
	return result, nil
}

func (r *accountServiceTestRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return accountcore.ErrAccountNotFound
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		account.Extra[key] = value
	}
	if r.updates == nil {
		r.updates = make(map[int64][]map[string]any)
	}
	r.updates[id] = append(r.updates[id], updates)
	return nil
}

func (r *accountServiceTestRepo) FindByExtraField(_ context.Context, key string, value any) ([]accountcore.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]accountcore.Record, 0)
	for _, account := range r.accounts {
		if account.Extra != nil && account.Extra[key] == value {
			result = append(result, *account)
		}
	}
	return result, nil
}
