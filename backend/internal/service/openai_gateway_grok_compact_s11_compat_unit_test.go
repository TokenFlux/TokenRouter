//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

func buildGrokCompactRequestBody(body []byte) ([]byte, error) {
	return grokBodyCodec().BuildGrokCompactRequestBody(body)
}
