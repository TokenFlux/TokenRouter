// NewID 在原 compaction 生成点调用，由外层注入以保持格式与生成时机。
package grok

type BodyCodec struct{ NewID func() string }

const ComposerImageBridgeVisionModel = "grok-build-0.1"
const ComposerImageBridgeMaxOutputTokens = 512
