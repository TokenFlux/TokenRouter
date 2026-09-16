// Grok 授权入口的旧管理能力仅作调用与账号投影；规则、队列和缓存由所属模块持有。
package legacybridge

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type GrokAccountOperations struct{ Source service.AdminService }

func (b GrokAccountOperations) Get(ctx context.Context, id int64) (*account.Record, error) {
	v, err := b.Source.GetAccount(ctx, id)
	return service.AccountRecordView(v), err
}
func (b GrokAccountOperations) Create(ctx context.Context, input *account.CreateAccountInput) (*account.Record, error) {
	v, err := b.Source.CreateAccount(ctx, input)
	return service.AccountRecordView(v), err
}
func (b GrokAccountOperations) Update(ctx context.Context, id int64, input *account.UpdateAccountInput) (*account.Record, error) {
	v, err := b.Source.UpdateAccount(ctx, id, input)
	return service.AccountRecordView(v), err
}
func (b GrokAccountOperations) ProxyURL(ctx context.Context, id int64) (string, bool, error) {
	v, err := b.Source.GetProxy(ctx, id)
	if err != nil || v == nil {
		return "", false, err
	}
	return v.URL(), true, nil
}
func (b GrokAccountOperations) ImportSnapshot(value *account.Record) account.AccountSnapshot {
	return service.AccountSnapshotView(service.AccountFromRecord(value))
}

// RunGrokImportTask 保留现有后台登记与日志标签，不在桥接中再建 worker 或等待规则。
func RunGrokImportTask(label string, work func()) {
	service.RunBackgroundTask(label, service.BackgroundCall0(work))
}
