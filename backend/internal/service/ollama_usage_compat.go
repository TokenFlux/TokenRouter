// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

type legacyOllamaRead struct{ source AccountRepository }

func (r legacyOllamaRead) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return AccountRecordView(v), err
}

type legacyOllamaStore struct {
	legacyOllamaRead
	writer ollamaCloudUsageRepository
}

func legacyOllamaRepository(repo AccountRepository) acctcore.OllamaAccountReader {
	if repo == nil {
		return nil
	}
	reader := legacyOllamaRead{repo}
	if writer, ok := repo.(ollamaCloudUsageRepository); ok {
		return legacyOllamaStore{reader, writer}
	}
	return reader
}
func (r legacyOllamaStore) ListOllamaCloudUsageGroupAccounts(ctx context.Context, anchors []*acctcore.Record) ([]acctcore.Record, error) {
	old := make([]*Account, len(anchors))
	for i, v := range anchors {
		old[i] = AccountFromRecord(v)
	}
	v, err := r.writer.ListOllamaCloudUsageGroupAccounts(ctx, old)
	return AccountRecordsView(v), err
}
func (r legacyOllamaStore) SaveOllamaCloudUsageSession(ctx context.Context, v *acctcore.Record, cipher string, enabled bool) error {
	return r.writer.SaveOllamaCloudUsageSession(ctx, AccountFromRecord(v), cipher, enabled)
}
func (r legacyOllamaStore) DeleteOllamaCloudUsageSession(ctx context.Context, v *acctcore.Record) error {
	return r.writer.DeleteOllamaCloudUsageSession(ctx, AccountFromRecord(v))
}
func (r legacyOllamaStore) SetOllamaCloudUsageAutoRefresh(ctx context.Context, v *acctcore.Record, enabled bool) error {
	return r.writer.SetOllamaCloudUsageAutoRefresh(ctx, AccountFromRecord(v), enabled)
}
func (r legacyOllamaStore) UpdateOllamaCloudUsageSnapshot(ctx context.Context, v *acctcore.Record, snapshot *acctcore.OllamaCloudUsageSnapshot) error {
	return r.writer.UpdateOllamaCloudUsageSnapshot(ctx, AccountFromRecord(v), snapshot)
}
func (r legacyOllamaStore) DisableOllamaCloudUsageAutoRefresh(ctx context.Context, v *acctcore.Record) error {
	return r.writer.DisableOllamaCloudUsageAutoRefresh(ctx, AccountFromRecord(v))
}
func (r legacyOllamaStore) ListDueOllamaCloudUsageAccounts(ctx context.Context, now time.Time, debounce, maxWait time.Duration, limit int) ([]acctcore.Record, error) {
	v, err := r.writer.ListDueOllamaCloudUsageAccounts(ctx, now, debounce, maxWait, limit)
	return AccountRecordsView(v), err
}
