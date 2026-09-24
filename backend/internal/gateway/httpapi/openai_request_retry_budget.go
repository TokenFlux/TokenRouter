// 旧入站仅在原 Gin key 中持有共享预算，算法和锁由原生状态唯一拥有。
package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

const openAIResponsesRejectedFieldRetryBudgetContextKey = "openai_responses_rejected_field_retry_budget"

// openAIResponsesRejectedFieldRetryStateForRequest returns a fresh loop guard
// for one account attempt backed by the inbound request's shared retry budget.
// A later account may apply the same compatibility transform, while all account
// attempts together remain bounded.
func openAIResponsesRejectedFieldRetryStateForRequest(c *gin.Context, initialBody []byte) *openai.ResponsesRejectedFieldRetryState {
	var budget *openai.ResponsesRejectedFieldRetryBudget
	if c != nil {
		if existing, ok := c.Get(openAIResponsesRejectedFieldRetryBudgetContextKey); ok {
			budget, _ = existing.(*openai.ResponsesRejectedFieldRetryBudget)
		}
	}
	if budget == nil {
		budget = &openai.ResponsesRejectedFieldRetryBudget{}
		if c != nil {
			c.Set(openAIResponsesRejectedFieldRetryBudgetContextKey, budget)
		}
	}
	return openai.NewOpenAIResponsesRejectedFieldRetryStateWithBudget(initialBody, budget)
}
