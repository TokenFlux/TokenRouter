package ws

import "fmt"

// GenericPolicyError 表示自定义错误码已开启但当前状态未命中。
// HTTP 入站路径据此返回统一 500，避免把不可信的握手或事件错误透传给客户端。
type GenericPolicyError struct {
	upstreamStatus int
}

func (e *GenericPolicyError) Error() string {
	if e == nil || e.upstreamStatus == 0 {
		return "upstream websocket error not in custom error codes"
	}
	return fmt.Sprintf("upstream websocket status %d not in custom error codes", e.upstreamStatus)
}

// NewGenericPolicyError 保留未命中自定义状态码时的内部错误，不向客户端泄露上游正文。
func NewGenericPolicyError(status int) error { return &GenericPolicyError{upstreamStatus: status} }
