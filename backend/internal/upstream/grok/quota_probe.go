// 主动额度探测在响应仍由本次操作持有时同步交付观测，保留原状态写入与错误处理顺序。
package grok

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type ActiveQuotaOptions struct {
	URL          string
	Body         []byte
	Token        string `json:"-"`
	AccountID    int64
	Model        string
	Timeout      time.Duration
	ApplyHeaders func(http.Header)
	Do           func(*http.Request) (*http.Response, error)
	Observe      func(*QuotaSnapshot, int)
	MapStatus    func(int) int
	Warn         func(string, ...any)
}

func (ActiveQuotaOptions) String() string     { return "grok active quota options" }
func (o ActiveQuotaOptions) GoString() string { return o.String() }
func FetchActiveQuota(ctx context.Context, options ActiveQuotaOptions) error {
	callCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, options.URL, bytes.NewReader(options.Body))
	if err != nil {
		return infraerrors.Newf(infraerrors.CategoryInternalServer, "GROK_QUOTA_PROBE_REQUEST_BUILD_FAILED", "failed to build upstream request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+options.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	options.ApplyHeaders(req.Header)
	resp, err := options.Do(req)
	if err != nil {
		return infraerrors.Newf(infraerrors.CategoryBadGateway, "GROK_QUOTA_PROBE_REQUEST_FAILED", "upstream probe failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	snapshot := ObserveQuotaHeaders(resp.Header, resp.StatusCode, "active_probe")
	options.Observe(snapshot, resp.StatusCode)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil
	}
	if resp.StatusCode >= 400 {
		const reason = "GROK_QUOTA_PROBE_UPSTREAM_ERROR"
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		options.Warn("grok_quota_probe_failed", "account_id", options.AccountID, "model", options.Model, "status", resp.StatusCode, "reason", reason)
		return infraerrors.Newf(infraerrors.Category(options.MapStatus(resp.StatusCode)), reason, "upstream returned %d for probe model %q", resp.StatusCode, options.Model)
	}
	return nil
}
