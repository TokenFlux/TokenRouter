package httpapi

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// MarkOpenAINativeCompactionV2 标记当前请求为原生 V2 压缩协议。
func MarkOpenAINativeCompactionV2(c *gin.Context) {
	if c != nil {
		c.Set(openAINativeCompactionV2Key, true)
	}
}

// IsOpenAINativeCompactionV2 返回当前请求是否被识别为原生远程 compaction v2。
func IsOpenAINativeCompactionV2(c *gin.Context) bool {
	return c != nil && c.GetBool(openAINativeCompactionV2Key)
}

func hasOpenAICodexBetaFeaturesHeader(h http.Header) bool {
	if h == nil {
		return false
	}
	for _, value := range h.Values("x-codex-beta-features") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// ApplyOpenAICodexBetaFeatures 对齐 Codex 的会话级协商行为：原生 V2 压缩请求
// 总会带 remote_compaction_v2；其它 OAuth Responses 请求仅在客户端未声明时补
// 默认能力。非 OAuth 上游不接收额外会话级能力头。
func ApplyOpenAICodexBetaFeatures(c *gin.Context, oauthLike bool, h http.Header) {
	if h == nil {
		return
	}
	if IsOpenAINativeCompactionV2(c) {
		openai.EnsureRemoteCompactionV2Header(h)
		return
	}
	if !oauthLike {
		return
	}
	if hasOpenAICodexBetaFeaturesHeader(h) {
		return
	}
	h.Set("x-codex-beta-features", openAIRemoteCompactionV2Feature)
}

const openAINativeCompactionV2Key = "openai_native_compaction_v2"
const openAIRemoteCompactionV2Feature = "remote_compaction_v2"
