// 兼容构造器委托 gateway 的唯一存储实现。
package repository

import (
	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/gateway/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewErrorPassthroughRepository(client *ent.Client) service.ErrorPassthroughRepository {
	return postgres.NewErrorPassthroughRepository(client)
}
