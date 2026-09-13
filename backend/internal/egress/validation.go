// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

// ValidationError 表示验证错误
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
