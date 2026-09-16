// 旧 Images 入口只选择账号开关和传输参数，回填实现归原生平台。
package service

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// AccountExtraImagesURLToB64JSON 是账户 extra 中的开关键。
const AccountExtraImagesURLToB64JSON = "images_url_to_b64_json"

// ImagesURLToB64JSONEnabled 返回账户是否开启 URL 到 base64 的图片回填。
func ImagesURLToB64JSONEnabled(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && account.getExtraBool(AccountExtraImagesURLToB64JSON)
}
func (s *OpenAIGatewayService) imageBackfillOptions(account *Account) native.ImageBackfillOptions {
	options := native.ImageBackfillOptions{Enabled: ImagesURLToB64JSONEnabled(account)}
	if s != nil {
		options.ValidateURL = s.validateOutboundURL
		if s.httpUpstream != nil && account != nil {
			proxyURL := ""
			if account.ProxyID != nil && account.Proxy != nil {
				proxyURL = account.Proxy.URL()
			}
			options.Do = func(request *http.Request) (*http.Response, error) {
				return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
			}
		}
	}
	if account != nil {
		options.Failure = func(index int) {
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Images b64_json backfill skipped account_id=%d index=%d: image download or conversion failed", account.ID, index)
		}
	}
	return options
}
func (s *OpenAIGatewayService) backfillOpenAIImagesB64JSON(ctx context.Context, account *Account, parsed *OpenAIImagesRequest, body []byte) []byte {
	options := s.imageBackfillOptions(account)
	if parsed != nil {
		options.Stream = parsed.Stream
		options.ResponseFormat = parsed.ResponseFormat
	}
	return options.Backfill(ctx, body)
}

func isBackfillImageContent(data []byte) bool { return native.IsBackfillImageContent(data) }
