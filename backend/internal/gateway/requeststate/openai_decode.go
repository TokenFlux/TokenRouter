package requeststate

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// DecodeOpenAIRequestBody 保留数字精度与原错误前缀，不读写 HTTP 上下文。
func DecodeOpenAIRequestBody(body []byte) (map[string]any, error) {
	var request map[string]any
	if err := wirejson.DecodeUseNumber(body, &request); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	return request, nil
}

// Decode 在调用方确实需要完整对象时解码视图当前持有的报文。
func (v OpenAIRequestView) Decode() (map[string]any, error) {
	return DecodeOpenAIRequestBody(v.Bytes())
}

// OpenAIRequestMetaFromBody 复用同一宽容字段视图，不触发完整 JSON 解码。
func OpenAIRequestMetaFromBody(body []byte) (model string, stream bool, promptCacheKey string) {
	view := NewOpenAIRequestView(body)
	return view.Model, view.Stream, view.PromptCacheKey
}
