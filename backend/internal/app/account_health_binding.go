package app

import "github.com/TokenFlux/TokenRouter/internal/service"

// bindAccountHealthRuntime 只把唯一原生拥有者交给尚未清零的执行端口。
func bindAccountHealthRuntime(source *service.RateLimitService, health *accountHealthRuntime) {
	if health == nil {
		return
	}
	source.BindHealth(health.Health)
	source.BindRecovery(health.Recovery)
	source.BindRateLimitObserver(health.Observer.Limits)
	source.BindTeamLinkedHealth(health.Observer.Team)
	source.BindUpstreamHealth(health.Observer)
}
