// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type legacyCRSStore struct{ source AccountRepository }

func (s legacyCRSStore) Create(ctx context.Context, value *acctcore.Record) error {
	v := AccountFromRecord(value)
	err := s.source.Create(ctx, v)
	*value = *AccountRecordView(v)
	return err
}
func (s legacyCRSStore) GetByCRSAccountID(ctx context.Context, id string) (*acctcore.Record, error) {
	v, err := s.source.GetByCRSAccountID(ctx, id)
	return AccountRecordView(v), err
}
func (s legacyCRSStore) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	return s.source.ListCRSAccountIDs(ctx)
}
func (s legacyCRSStore) ListShadowsByParent(ctx context.Context, id int64) ([]*acctcore.Record, error) {
	v, err := s.source.ListShadowsByParent(ctx, id)
	if v == nil {
		return nil, err
	}
	out := make([]*acctcore.Record, len(v))
	for i := range v {
		out[i] = AccountRecordView(v[i])
	}
	return out, err
}
func (s legacyCRSStore) Update(ctx context.Context, value *acctcore.Record) error {
	v := AccountFromRecord(value)
	err := s.source.Update(ctx, v)
	*value = *AccountRecordView(v)
	return err
}
func (s legacyCRSStore) UpdateConfiguration(ctx context.Context, value *acctcore.Record, change acctcore.ConfigurationChange) error {
	v := AccountFromRecord(value)
	err := updateAccountConfiguration(ctx, s.source, v, change)
	*value = *AccountRecordView(v)
	return err
}
