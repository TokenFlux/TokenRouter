// 旧后台构造器只完成参数投影，S15/S16 清理。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/redis/go-redis/v9"
)

type OpsAlertEvaluatorService = ops.OpsAlertEvaluatorService

func NewOpsAlertEvaluatorService(s *OpsService, repo OpsRepository, email *EmailService, r *redis.Client, cfg *config.Config, proxy ProxyRepository) *OpsAlertEvaluatorService {
	return ops.NewOpsAlertEvaluatorService(s, repo, LegacyOpsEmail(email), rediscache.NewRuntime(r), LegacyOpsOptions(cfg), proxy)
}
