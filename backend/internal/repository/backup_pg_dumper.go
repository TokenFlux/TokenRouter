package repository

import (
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// NewPgDumper 保留旧构造签名，数据库参数由装配投影。
func NewPgDumper(cfg *config.Config) service.DBDumper {
	d := cfg.Database
	return bp.NewPgDumper(bp.DatabaseOptions{Host: d.Host, Port: d.Port, User: d.User, Password: d.Password, DBName: d.DBName, SSLMode: d.SSLMode})
}
