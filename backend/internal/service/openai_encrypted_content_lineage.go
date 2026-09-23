package service

import (
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/gin-gonic/gin"
)

// invalid_encrypted_content 失效密文 lineage。
//
// 上游判定某轮请求中的加密 reasoning/compaction 项不可解后，同一失效密文会随
// 客户端维护的会话历史在后续每一轮重新出现，重复触发"整包被拒→剥离→重试/
// 重连"。这里按会话记录已被上游拒绝过的 encrypted_content 摘要
// （OpenAIWSStateStore，带 TTL 与容量自保护）；后续请求进场时仅剥离摘要命中
// 的项，新生成的密文摘要不同，不会被误删。

const openAIWSFallbackReasonInvalidEncryptedContent = "invalid_encrypted_content"

// openAIWSIngressSessionHashContextKey 在 gin context 中携带 ingress 会话哈希，
// 供 HTTP bridge turn 内的 lineage 记录复用同一会话键。
const openAIWSIngressSessionHashContextKey = "openai_ws_ingress_session_hash"

// markOpenAIWSInvalidEncryptedContentLineage 把本次被上游拒绝的密文摘要写入
// 会话 lineage。digests 须在剥离前收集。
func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineage(groupID int64, sessionHash string, digests []string) {
	if s == nil || len(digests) == 0 || strings.TrimSpace(sessionHash) == "" {
		return
	}
	stateStore := s.ResponseStateStore()
	if stateStore == nil {
		return
	}
	stateStore.MarkSessionInvalidEncryptedContent(groupID, sessionHash, digests, s.selection.SessionStickyTTL())
}

// sessionInvalidEncryptedContentDigests 返回会话已知失效密文摘要；全局无记录
// 时（常态）零成本返回 nil。
func (s *OpenAIGatewayService) sessionInvalidEncryptedContentDigests(groupID int64, sessionHash string) map[string]struct{} {
	if s == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	stateStore := s.ResponseStateStore()
	if stateStore == nil || !stateStore.HasAnySessionInvalidEncryptedContent() {
		return nil
	}
	return stateStore.GetSessionInvalidEncryptedContentDigests(groupID, sessionHash)
}

// openAIWSLineageSessionHashFromContext 取 lineage 会话键：优先 ingress 循环
// 写入的会话哈希（与读取侧同键），否则按请求体派生。
func (s *OpenAIGatewayService) openAIWSLineageSessionHashFromContext(c *gin.Context, body []byte) string {
	if c != nil {
		if fromCtx := strings.TrimSpace(c.GetString(openAIWSIngressSessionHashContextKey)); fromCtx != "" {
			return fromCtx
		}
	}
	return gatewayhttp.GenerateOpenAISessionHash(c, body)
}

// markOpenAIWSInvalidEncryptedContentLineageFromPayload 在上游以
// invalid_encrypted_content 拒绝 payload 时记录其密文摘要并输出观测日志。
func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineageFromPayload(
	c *gin.Context,
	payload []byte,
	logKey string,
	accountID int64,
	turn int,
) {
	digests := openai.CollectOpenAIEncryptedContentDigestsRaw(payload)
	if len(digests) == 0 {
		return
	}
	s.markOpenAIWSInvalidEncryptedContentLineage(
		getOpenAIGroupIDFromContext(c),
		s.openAIWSLineageSessionHashFromContext(c, payload),
		digests,
	)
	gatewayprovider.LogOpenAIWSModeInfo("%s account_id=%d turn=%d digests=%d", logKey, accountID, turn, len(digests))
}

// stripSessionInvalidEncryptedContentLogged 对 payload 执行会话失效密文剥离并
// 输出观测日志（logKey / logKey+"_skip"），返回（可能已替换的）payload 与剥离
// 项数；未命中或剥离失败时原样返回。
func (s *OpenAIGatewayService) stripSessionInvalidEncryptedContentLogged(
	payload []byte,
	invalid map[string]struct{},
	logKey string,
	accountID int64,
	turn int,
) ([]byte, int) {
	strippedPayload, strippedCount, stripErr := openai.StripOpenAIInvalidEncryptedContentRaw(payload, invalid)
	if stripErr != nil {
		gatewayprovider.LogOpenAIWSModeInfo(
			"%s_skip account_id=%d turn=%d reason=strip_error cause=%s",
			logKey,
			accountID,
			turn, gatewayprovider.TruncateOpenAIWSLogValue(stripErr.Error(), gatewayprovider.OpenAIWSLogValueMaxLen),
		)
		return payload, 0
	}
	if strippedCount > 0 {
		gatewayprovider.LogOpenAIWSModeInfo(
			"%s account_id=%d turn=%d stripped_items=%d",
			logKey,
			accountID,
			turn,
			strippedCount,
		)
	}
	return strippedPayload, strippedCount
}
