package httpclient

import (
	"net/http"

	"github.com/imroc/req/v3"
)

// CloseIdleConnections 在请求与后台任务完成后释放本池的空闲连接。
// 保留客户端键和选项，也不取消仍由调用方拥有的响应体。
func (s *UpstreamPool) CloseIdleConnections() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.clients {
		if entry != nil && entry.client != nil {
			entry.client.CloseIdleConnections()
		}
	}
}

// CloseSharedIdleConnections 保持通用和 req 两个缓存空间独立，只释放传输资源。
func CloseSharedIdleConnections() {
	sharedClients.Range(func(_, value any) bool {
		if client, ok := value.(*http.Client); ok {
			client.CloseIdleConnections()
		}
		return true
	})
	sharedReqClients.Range(func(_, value any) bool {
		if client, ok := value.(*req.Client); ok {
			client.GetClient().CloseIdleConnections()
		}
		return true
	})
}
