//go:build unit

package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// HTTP 故障切换夹具只投影存储返回值，不复制刷新行为。
type grokCredentialTokenReader struct{ source *grokCredentialHandlerRepo }

func (r grokCredentialTokenReader) GetByID(ctx context.Context, id int64) (*account.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return service.AccountRecordView(v), err
}
