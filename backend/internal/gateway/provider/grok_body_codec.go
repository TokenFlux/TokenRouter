package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/google/uuid"
)

// GrokBodyCodec 保留原随机 ID 的生成时机，不持有跨请求状态。
func GrokBodyCodec() grok.BodyCodec { return grok.BodyCodec{NewID: uuid.NewString} }
