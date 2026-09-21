package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type openAIProbeStore struct {
	openAIProbeRecords
	updateExtraCalls   chan map[string]any
	updatedExtra       map[string]any
	bulkUpdatedIDs     []int64
	bulkUpdatedPayload accountcore.AccountBulkUpdate
	rateLimitedID      int64
	rateLimitedAt      *time.Time
	clearedErrorID     int64
	setErrorID         int64
	setErrorMsg        string
}

func (r *openAIProbeStore) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updatedExtra = updates
	if r.updateExtraCalls != nil {
		copy := make(map[string]any, len(updates))
		for key, value := range updates {
			copy[key] = value
		}
		r.updateExtraCalls <- copy
	}
	return nil
}

func (r *openAIProbeStore) BulkUpdate(_ context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	r.bulkUpdatedIDs = append([]int64(nil), ids...)
	r.bulkUpdatedPayload = updates
	return int64(len(ids)), nil
}

func (r *openAIProbeStore) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.rateLimitedAt = &resetAt
	return nil
}

func (r *openAIProbeStore) ClearError(_ context.Context, id int64) error {
	r.clearedErrorID = id
	return nil
}

func (r *openAIProbeStore) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

// 输出夹具不保存 Gin 状态，使用真实请求和响应记录器。
type openAIProbeOutput struct {
	Request  *http.Request
	recorder *httptest.ResponseRecorder
}

type openAIProbeRecords struct{ accountsByID map[int64]*accountcore.Record }

func (r openAIProbeRecords) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	value, ok := r.accountsByID[id]
	if !ok {
		return nil, fmt.Errorf("account %d missing", id)
	}
	return accountcore.CloneRecord(value), nil
}
