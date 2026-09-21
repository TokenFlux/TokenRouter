package forward

// UpstreamWarning 保存上游风控警告事实；是否结算与如何响应仍由完成及 HTTP 边界决定。
type UpstreamWarning struct {
	StatusCode   int
	ResponseBody []byte
	Message      string
}

// UpstreamWarningCarrier 让既有错误链携带同一警告值，不复制平台状态。
type UpstreamWarningCarrier interface {
	OpenAIUpstreamWarning() *UpstreamWarning
}
