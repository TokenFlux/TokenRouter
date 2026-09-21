package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ExecutionGroups 只读取执行时的分组协议配置，创作用例不接收完整分组实体。
type ExecutionGroups interface {
	GetByIDLite(context.Context, int64) (*routing.Group, error)
}
