package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/gin-gonic/gin"
)

// 请求级 hint 仅限 HTTP：缺失表示 unknown，false/true 都表示已完成 canonical 判定。
const openAIImageIntentHintContextKey = "openai_image_intent_hint"

type ImageIntentClassifier func(endpoint string, requestedModel string, body []byte) bool

// SetOpenAIImageIntentHint 只写入请求级 canonical 判定，不记录 attempt-local 结果。
func SetOpenAIImageIntentHint(c *gin.Context, imageIntent bool) {
	if c == nil || GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP {
		return
	}
	c.Set(openAIImageIntentHintContextKey, imageIntent)
}

func GetOpenAIImageIntentHint(c *gin.Context) (imageIntent bool, known bool) {
	if c == nil || GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP {
		return false, false
	}
	value, ok := c.Get(openAIImageIntentHintContextKey)
	if !ok {
		return false, false
	}
	imageIntent, ok = value.(bool)
	return imageIntent, ok
}

func ResolveOpenAIImageIntentHint(
	c *gin.Context,
	requestedModel string,
	canonicalBody []byte,
	classify ImageIntentClassifier,
) bool {
	if imageIntent, known := GetOpenAIImageIntentHint(c); known {
		return imageIntent
	}
	imageIntent := classify(media.OpenAIResponsesEndpoint, requestedModel, canonicalBody)
	SetOpenAIImageIntentHint(c, imageIntent)
	return imageIntent
}

func ResolveOpenAIPassthroughImageIntent(
	c *gin.Context,
	canonicalRequestedModel string,
	canonicalBody []byte,
	attemptRequestedModel string,
	attemptBody []byte,
	attemptInvalidated bool,
	classify ImageIntentClassifier,
) bool {
	imageIntent := ResolveOpenAIImageIntentHint(c, canonicalRequestedModel, canonicalBody, classify)
	if attemptInvalidated {
		// strip/compact 改写只重算当前 attempt，不得把变换后的结果写回请求级 canonical hint。
		imageIntent = classify(media.OpenAIResponsesEndpoint, attemptRequestedModel, attemptBody)
	}
	return imageIntent
}
