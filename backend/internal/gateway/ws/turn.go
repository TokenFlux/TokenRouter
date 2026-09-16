// Package ws 拥有入站会话的每轮快照与执行生命周期。
package ws

import (
	"sync"
	"time"
)

type TurnPayload struct {
	StartedAt                time.Time
	RequestBody              []byte
	OriginalModel            string
	RoutingModel             string
	UpstreamModel            string
	ServiceTier              *string
	ReasoningEffort          *string
	RequestedReasoningEffort *string
	PreviousResponseID       string
	Source                   string
}

type TurnPayloadQueue struct {
	mu    sync.Mutex
	items []TurnPayload
}

func NewTurnPayloadQueue() *TurnPayloadQueue {
	return &TurnPayloadQueue{}
}

func (q *TurnPayloadQueue) Push(item TurnPayload) {
	if q == nil {
		return
	}
	item.RequestBody = append([]byte(nil), item.RequestBody...)
	if item.ServiceTier != nil {
		value := *item.ServiceTier
		item.ServiceTier = &value
	}
	if item.ReasoningEffort != nil {
		value := *item.ReasoningEffort
		item.ReasoningEffort = &value
	}
	if item.RequestedReasoningEffort != nil {
		value := *item.RequestedReasoningEffort
		item.RequestedReasoningEffort = &value
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, item)
}

// Len 返回尚未收到终态事件的 turn 数量。
func (q *TurnPayloadQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Peek 返回当前 turn 的请求上下文但不出队，用于上游错误事件先于 turn 完成时复用同一模型。
func (q *TurnPayloadQueue) Peek() TurnPayload {
	if q == nil {
		return TurnPayload{Source: "passthrough_missing"}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return TurnPayload{Source: "passthrough_missing"}
	}
	return q.items[0]
}

func (q *TurnPayloadQueue) Pop() TurnPayload {
	if q == nil {
		return TurnPayload{Source: "passthrough_missing"}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return TurnPayload{Source: "passthrough_missing"}
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item
}
