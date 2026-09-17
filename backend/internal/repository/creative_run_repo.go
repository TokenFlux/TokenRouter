// 旧仓储入口委托任务所属存储，S15/S16 清理。
package repository

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	native "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func NewCreativeRunRepository(client *dbent.Client) service.CreativeRunRepository {
	return native.NewCreativeRunRepository(client)
}
