package provider

import (
	"log"
	"sync"
)

// PricingWarnings 保留每个计费实例的告警去重状态，纯计算只返回来源标记。
type PricingWarnings struct{ fallbackSeen sync.Map }

func NewPricingWarnings() *PricingWarnings {
	return &PricingWarnings{}
}

// Fallback 只在第一次使用模型回退价时输出原日志。
func (w *PricingWarnings) Fallback(model string) {
	if _, seen := w.fallbackSeen.LoadOrStore(model, struct{}{}); !seen {
		log.Printf("[Billing] Using fallback pricing for model: %s", model)
	}
}
