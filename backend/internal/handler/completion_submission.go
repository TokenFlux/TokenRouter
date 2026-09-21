// 旧装配仅选择已有提交策略，队列、快照和降级算法均由原生实现持有。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

func (h *GatewayHandler) submitUsageRecordTask(c *gin.Context, task completion.UsageRecordTask) {
	gatewayhttp.NewCompletionSubmission(h.usageRecordWorkerPool, false).Submit(c, task)
}
func (h *GatewayHandler) submitMandatoryUsageRecordTask(c *gin.Context, task completion.UsageRecordTask) {
	gatewayhttp.NewCompletionSubmission(h.usageRecordWorkerPool, false).SubmitMandatory(c, task)
}

func (h *OpenAIGatewayHandler) submitMandatoryUsageRecordTask(c *gin.Context, task completion.UsageRecordTask) {
	gatewayhttp.NewCompletionSubmission(h.usageRecordWorkerPool, true).SubmitMandatory(c, task)
}
func (h *OpenAIGatewayHandler) submitOpenAIUsageRecordTask(c *gin.Context, result *forwardcore.OpenAIResult, task completion.UsageRecordTask) {
	images := 0
	if result != nil {
		images = result.ImageCount
	}
	gatewayhttp.NewCompletionSubmission(h.usageRecordWorkerPool, true).SubmitImages(c, images, task)
}
func (h *QoderGatewayHandler) submitUsageRecordTask(c *gin.Context, task completion.UsageRecordTask) {
	gatewayhttp.NewQoderCompletionSubmission(h.usageRecordWorkerPool).Submit(c, task)
}
