package repository

import (
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// NewS3BackupStoreFactory 委托唯一技术实现。
func NewS3BackupStoreFactory() service.BackupObjectStoreFactory { return bp.NewS3BackupStoreFactory() }
