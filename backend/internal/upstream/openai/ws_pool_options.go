// WS 池只接收技术容量参数和无凭据的账号投影；保持原缺省及显式零值语义。
package openai

import (
	"math"
	"time"
)

const WSConnMaxAge = 60 * time.Minute

type WSPoolAccount struct {
	FingerprintMode string
	ID              int64
	Concurrency     int
	Type            string
}
type WSPoolOptions struct {
	MaxConnsPerAccount                                      int
	DynamicMaxConnsByAccountConcurrencyEnabled              bool
	ModeRouterV2Enabled                                     bool
	OAuthMaxConnsFactor, APIKeyMaxConnsFactor               float64
	MinIdlePerAccount, MaxIdlePerAccount, QueueLimitPerConn int
	PoolTargetUtilization                                   float64
	PrewarmCooldownMS, DialTimeoutSeconds                   int
}

func (p *WSPoolOptions) MaxConnsHardCap() int {
	if p != nil && p.MaxConnsPerAccount > 0 {
		return p.MaxConnsPerAccount
	}
	return 8
}
func (p *WSPoolOptions) DynamicMaxConnsEnabled() bool {
	if p != nil {
		return p.DynamicMaxConnsByAccountConcurrencyEnabled
	}
	return false
}
func (p *WSPoolOptions) UseModeRouterV2() bool {
	if p != nil {
		return p.ModeRouterV2Enabled
	}
	return false
}
func (p *WSPoolOptions) MaxConnsFactorByAccount(account *WSPoolAccount) float64 {
	if p == nil || account == nil {
		return 1.0
	}
	switch account.Type {
	case "oauth":
		if p.OAuthMaxConnsFactor > 0 {
			return p.OAuthMaxConnsFactor
		}
	case "apikey":
		if p.APIKeyMaxConnsFactor > 0 {
			return p.APIKeyMaxConnsFactor
		}
	}
	return 1.0
}
func (p *WSPoolOptions) EffectiveMaxConnsByAccount(account *WSPoolAccount) int {
	hardCap := p.MaxConnsHardCap()
	if hardCap <= 0 {
		return 0
	}
	if p.UseModeRouterV2() {
		if account == nil {
			return hardCap
		}
		if account.Concurrency <= 0 {
			return 0
		}
		return min(account.Concurrency, hardCap)
	}
	if account == nil || !p.DynamicMaxConnsEnabled() {
		return hardCap
	}
	if account.Concurrency <= 0 {
		// 0/-1 等“无限制”并发场景下，仍由全局硬上限兜底。
		return hardCap
	}
	factor := p.MaxConnsFactorByAccount(account)
	if factor <= 0 {
		factor = 1.0
	}
	effective := int(math.Ceil(float64(account.Concurrency) * factor))
	if effective < 1 {
		effective = 1
	}
	if effective > hardCap {
		effective = hardCap
	}
	return effective
}
func (p *WSPoolOptions) MinIdle() int {
	if p != nil && p.MinIdlePerAccount >= 0 {
		return p.MinIdlePerAccount
	}
	return 0
}
func (p *WSPoolOptions) MaxIdle() int {
	if p != nil && p.MaxIdlePerAccount >= 0 {
		return p.MaxIdlePerAccount
	}
	return 4
}
func (p *WSPoolOptions) MaxConnAge() time.Duration {
	return WSConnMaxAge
}
func (p *WSPoolOptions) QueueLimit() int {
	if p != nil && p.QueueLimitPerConn > 0 {
		return p.QueueLimitPerConn
	}
	return 256
}
func (p *WSPoolOptions) TargetUtilization() float64 {
	if p != nil {
		ratio := p.PoolTargetUtilization
		if ratio > 0 && ratio <= 1 {
			return ratio
		}
	}
	return 0.7
}
func (p *WSPoolOptions) PrewarmCooldown() time.Duration {
	if p != nil && p.PrewarmCooldownMS > 0 {
		return time.Duration(p.PrewarmCooldownMS) * time.Millisecond
	}
	return 0
}
func (p *WSPoolOptions) DialTimeout() time.Duration {
	if p != nil && p.DialTimeoutSeconds > 0 {
		return time.Duration(p.DialTimeoutSeconds) * time.Second
	}
	return 10 * time.Second
}
