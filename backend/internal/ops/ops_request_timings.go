// 本文件拥有 Ops 观测用例；配置与外部状态经端口注入。
package ops

import (
	"context"
	"strings"
)

// LookupRequestTimings 返回请求对应的 http.access 阶段耗时。
// Ops 未启用或当前仓储不支持该查询时按空结果处理，不影响使用记录主查询。
func (s *OpsService) LookupRequestTimings(ctx context.Context, clientRequestIDs []string) (map[string]*OpsRequestTiming, error) {
	result := make(map[string]*OpsRequestTiming)
	if s == nil || s.opsRepo == nil || len(clientRequestIDs) == 0 || !s.IsMonitoringEnabled(ctx) {
		return result, nil
	}
	for i := range clientRequestIDs {
		clientRequestIDs[i] = strings.TrimSpace(clientRequestIDs[i])
	}
	return s.opsRepo.ListRequestTimings(ctx, clientRequestIDs)
}
