// 本文件仅适配旧快照数据形状与调用，不持有调度规则、缓存或锁。
package service

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

func LegacySnapshotWrap(value *gatewayprovider.ExecutionAccount) scheduler.SnapshotAccount {
	return codec.WrapRecord(gatewayprovider.ExecutionRecord(value))
}
func LegacySnapshotWrapValues(values []gatewayprovider.ExecutionAccount) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i := range values {
		value := values[i]
		out[i] = LegacySnapshotWrap(&value)
	}
	return out
}
func LegacySnapshotWrapPointers(values []*gatewayprovider.ExecutionAccount) []scheduler.SnapshotAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.SnapshotAccount, len(values))
	for i, v := range values {
		out[i] = LegacySnapshotWrap(v)
	}
	return out
}
func LegacySnapshotValue(value scheduler.SnapshotAccount) (*gatewayprovider.ExecutionAccount, error) {
	record, err := codec.RecordValue(value)
	return gatewayprovider.NewExecutionAccount(record), err
}
func LegacySnapshotValues(values []scheduler.SnapshotAccount) ([]gatewayprovider.ExecutionAccount, error) {
	if values == nil {
		return nil, nil
	}
	out := make([]gatewayprovider.ExecutionAccount, 0, len(values))
	for _, v := range values {
		a, err := LegacySnapshotValue(v)
		if err != nil {
			return nil, err
		}
		if a != nil {
			out = append(out, *a)
		}
	}
	return out, nil
}
