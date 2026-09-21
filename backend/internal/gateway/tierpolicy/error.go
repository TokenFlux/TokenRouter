package tierpolicy

// BlockedError 表示服务档位策略明确拒绝本次请求；只承载既有安全错误消息。
type BlockedError struct{ Message string }

func (e *BlockedError) Error() string { return e.Message }
