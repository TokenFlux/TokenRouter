// 旧会话入口只投影请求数据并委托，状态只在 upstream/qoder 持有。
package service

import (
	"time"

	"github.com/gin-gonic/gin"

	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type qoderConversationStore = qoder.QoderConversationStore
type qoderConversationState = qoder.QoderConversationState
type qoderConversationPlan = qoder.QoderConversationPlan

func newQoderConversationStore(ttl time.Duration) *qoderConversationStore {
	return qoder.NewQoderConversationStore(ttl)
}

func cloneQoderConversationState(state *qoderConversationState) *qoderConversationState {
	return qoder.CloneQoderConversationState(state)
}
func qoderConversationStateEqual(a, b *qoderConversationState) bool {
	return qoder.QoderConversationStateEqual(a, b)
}
func qoderConversationKey(c *gin.Context, account *Account, protocol string, request qoderPayloadRequest) (string, string) {
	return qoder.QoderConversationKey(qoderRequestMetadata(c), qoderAccountID(account), protocol, request)
}
func qoderAccountScopedConversationKey(account *Account, key string) string {
	return qoder.QoderAccountScopedConversationKey(qoderAccountID(account), key)
}

func qoderAccountID(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}
func qoderRequestMetadata(c *gin.Context) qoder.RequestMetadata {
	result := qoder.RequestMetadata{APIKeyID: getAPIKeyIDFromContext(c)}
	if c != nil && c.Request != nil {
		result.Headers = c.Request.Header.Clone()
		result.ClaudeCode = IsClaudeCodeClient(c.Request.Context())
	}
	return result
}
