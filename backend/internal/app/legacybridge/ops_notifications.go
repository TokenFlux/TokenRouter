// Ops 通知桥接仅转发旧通知能力，不持有模板规则或发送状态。
package legacybridge

import (
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func OpsEmail(s *service.EmailService) *ops.EmailDelivery { return service.LegacyOpsEmail(s) }
