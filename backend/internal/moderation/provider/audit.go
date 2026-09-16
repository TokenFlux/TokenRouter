// AuditClient 执行单次审核 HTTP 交换，响应体由它关闭。
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

type AuditClient struct{ client *http.Client }

func NewAuditClient() *AuditClient { return &AuditClient{client: timing.InstrumentClient(nil)} }
func (s *AuditClient) Execute(ctx context.Context, endpoint, key string, raw []byte, proxy func() (string, error), output any) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	url, err := proxy()
	if err != nil {
		return 0, nil, err
	}
	client := s.client
	if url != "" {
		client, err = httpclient.GetClient(httpclient.Options{ProxyURL: url})
		if err != nil {
			return 0, nil, fmt.Errorf("build moderation proxy client: %w", err)
		}
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, body, nil
	}
	err = json.NewDecoder(resp.Body).Decode(output)
	return resp.StatusCode, nil, err
}
