// 旧客户端构造只投影参数，技术实现由 Ops provider 唯一提供。
package repository

import (
	"os"

	"github.com/TokenFlux/TokenRouter/internal/ops/provider"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewGitHubReleaseClient(proxyURL string, allow bool) service.GitHubReleaseClient {
	return provider.NewReleaseClient(provider.ReleaseOptions{ProxyURL: proxyURL, AllowDirectOnProxyError: allow, GitHubToken: os.Getenv("UPDATE_GITHUB_TOKEN")})
}
