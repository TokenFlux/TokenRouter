// Images HTTP 适配选择账号开关和传输参数，回填算法由上游包实现。
package httpapi

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func (s *OpenAIImagesExecutor) imageBackfillOptions(account *gatewayprovider.ExecutionAccount) openai.ImageBackfillOptions {
	options := openai.ImageBackfillOptions{Enabled: gatewayprovider.ImagesURLToB64JSONEnabled(account)}
	if s != nil {
		options.ValidateURL = s.Requests.ValidateURL
		if s.Requests.Transport != nil && account != nil {
			proxyURL := ""
			if account.Record.ProxyID != nil && account.Record.Proxy != nil {
				proxyURL = account.Record.Proxy.URL()
			}
			options.Do = func(request *http.Request) (*http.Response, error) {
				return s.Requests.Transport.Do(request, proxyURL, account.Record.ID, account.Record.Concurrency)
			}
		}
	}
	if account != nil {
		options.Failure = func(index int) {
			logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images b64_json backfill skipped account_id=%d index=%d: image download or conversion failed", account.Record.ID, index)
		}
	}
	return options
}
func (s *OpenAIImagesExecutor) backfillOpenAIImagesB64JSON(ctx context.Context, account *gatewayprovider.ExecutionAccount, parsed *media.ImageRequest, body []byte) []byte {
	options := s.imageBackfillOptions(account)
	if parsed != nil {
		options.Stream = parsed.Stream
		options.ResponseFormat = parsed.ResponseFormat
	}
	return options.Backfill(ctx, body)
}
