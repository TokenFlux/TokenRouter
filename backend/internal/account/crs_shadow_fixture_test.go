//go:build unit

package account_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 影子契约只需原生账号行的内存存取，不构造旧管理员服务。
type crsShadowStore struct {
	rows map[int64]*account.Record
}

func newCRSShadowStore() *crsShadowStore {
	return &crsShadowStore{rows: make(map[int64]*account.Record)}
}

func (s *crsShadowStore) Create(_ context.Context, record *account.Record) error {
	record.ID = int64(len(s.rows) + 1)
	s.rows[record.ID] = record
	return nil
}

func (s *crsShadowStore) GetByID(_ context.Context, id int64) (*account.Record, error) {
	return s.rows[id], nil
}

func (s *crsShadowStore) ListShadowsByParent(_ context.Context, id int64) ([]*account.Record, error) {
	var rows []*account.Record
	for _, row := range s.rows {
		if row.ParentAccountID != nil && *row.ParentAccountID == id {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *crsShadowStore) Update(_ context.Context, record *account.Record) error {
	s.rows[record.ID] = record
	return nil
}

func (s *crsShadowStore) UpdateConfiguration(ctx context.Context, record *account.Record, _ account.ConfigurationChange) error {
	return s.Update(ctx, record)
}
