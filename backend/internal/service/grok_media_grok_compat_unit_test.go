//go:build unit

// 迁移后的私有测试转接不进入生产构建。
package service

func grokMediaSignedVideoContentURL(body []byte, requestID string) (string, error) {
	return grokMediaCodec().GrokMediaSignedVideoContentURL(body, requestID)
}
