package repository

import (
	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingspostgres "github.com/TokenFlux/TokenRouter/internal/settings/postgres"
)

// NewSettingRepository 为旧消费者提供同一个 Store，S15/S16 清理旧名称。
func NewSettingRepository(client *ent.Client) settings.Repository {
	return settings.New(settingspostgres.NewSettingRepository(client))
}
