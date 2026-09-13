// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"time"
)

type GroupCapacityService = routing.CapacityService
type GroupCapacitySummary = routing.GroupCapacitySummary
type GroupAccountCapacityRow = account.GroupAccountCapacityRow

// OpenAIQuotaAutoPauseSettingsReader 提供 OpenAI 配额自动暂停的全局默认阈值。
type OpenAIQuotaAutoPauseSettingsReader interface {
	GetOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings
}

func AccountCapacitySnapshots(ctx context.Context, values []Account, settings OpenAIQuotaAutoPauseSettingsReader) []account.CapacitySnapshot {
	if len(values) == 0 {
		return nil
	}
	snapshot := AccountCapacitySettings(ctx, settings)
	out := make([]account.CapacitySnapshot, len(values))
	for i := range values {
		a := &values[i]
		out[i] = account.ProjectObservedCapacity(account.GroupAccountCapacityRow{AccountID: a.ID, Platform: a.Platform, Concurrency: a.Concurrency, Extra: a.Extra, SessionWindowStart: a.SessionWindowStart, SessionWindowEnd: a.SessionWindowEnd}, snapshot, time.Now())
	}
	return out
}
func AccountCapacityRows(ctx context.Context, rows []GroupAccountCapacityRow, settings OpenAIQuotaAutoPauseSettingsReader) []routing.CapacityAccountRow {
	if len(rows) == 0 {
		return nil
	}
	snapshot := AccountCapacitySettings(ctx, settings)
	out := make([]routing.CapacityAccountRow, len(rows))
	for i, row := range rows {
		out[i] = routing.CapacityAccountRow{GroupID: row.GroupID, Account: account.ProjectObservedCapacity(row, snapshot, time.Now())}
	}
	return out
}

// AccountCapacitySettings 仅读取原设置或请求上下文，S07/S11 更换上下文来源。
func AccountCapacitySettings(ctx context.Context, reader OpenAIQuotaAutoPauseSettingsReader) account.QuotaAutoPauseSettings {
	if reader != nil {
		return reader.GetOpenAIQuotaAutoPauseSettings(ctx)
	}
	return openAIQuotaAutoPauseSettingsFromContext(ctx)
}
