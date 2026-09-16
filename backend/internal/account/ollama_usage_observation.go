package account

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// OllamaUsageFetchInput 仅用于受控的出站端口，不作为管理响应或普通日志字段。
type OllamaUsageFetchInput struct {
	AccountID   int64
	Concurrency int
	ProxyURL    string `json:"-"`
	Cookie      string `json:"-"`
	ObservedAt  time.Time
}

type OllamaUsageObservation = usageview.OllamaUsageObservation
