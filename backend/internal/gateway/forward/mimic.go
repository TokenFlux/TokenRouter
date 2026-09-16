package forward

import "context"

// MimicPorts 提供平台格式与设置读取，流程不接收旧账号或动态 JSON 对象。
type MimicPorts interface {
	SystemSettings(context.Context) (bool, string, string)
	RewriteMimicSystem([]byte, string, string, string) []byte
	MimicMetadata(context.Context, []byte) string
	NormalizeOAuth([]byte, string, NormalizeOptions) ([]byte, string)
	RewriteCache(context.Context, []byte) []byte
	RewriteTools([]byte) ([]byte, bool)
	BindTools()
	ToolsLast([]byte) []byte
}

// Mimic 保留兼容入口的系统、指纹、消息缓存和工具改写顺序及失败降级。
func Mimic(ctx context.Context, p MimicPorts, eligible bool, body []byte, model string) []byte {
	if !eligible || len(body) == 0 {
		return body
	}
	enabled, prompt, blocks := p.SystemSettings(ctx)
	rewritten := false
	if enabled {
		body = p.RewriteMimicSystem(body, model, prompt, blocks)
		rewritten = true
	}
	options := NormalizeOptions{StripSystemCacheControl: !rewritten}
	if metadata := p.MimicMetadata(ctx, body); metadata != "" {
		options.InjectMetadata = true
		options.MetadataUserID = metadata
	}
	body, _ = p.NormalizeOAuth(body, model, options)
	body = p.RewriteCache(ctx, body)
	if next, found := p.RewriteTools(body); found {
		body = next
		p.BindTools()
	} else {
		body = p.ToolsLast(body)
	}
	return body
}
