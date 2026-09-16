// 大文件 Batch 传输沿用原响应头上限，不设置整体 Timeout。
package httpclient

import (
	"net/http"
	"time"
)

// batchImageDefaultHTTPClient 返回带连接/握手/响应头超时的共享客户端。
// 不设整体 Timeout：大文件上传与结果流式下载耗时不可预估，
// 但拨号、TLS、等待响应头必须有界，否则挂死的连接会无限占用提交路径。
func DefaultBatchHTTPClient() *http.Client {
	client, err := GetClient(Options{
		ResponseHeaderTimeout: 60 * time.Second,
	})
	if err != nil {
		return http.DefaultClient
	}
	return client
}
