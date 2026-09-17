package admin

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/backup"

	bh "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type BackupHandler = bh.BackupHandler

// NewBackupHandler 兼容旧构造；生产由 app 直接绑定新身份投影。
func NewBackupHandler(s *service.BackupService, u *service.UserService) *BackupHandler {
	var core *backup.BackupService
	if s != nil {
		core = s.BackupService
	}
	return bh.NewBackupHandler(core, func(ctx context.Context, id int64, password string) (bool, error) {
		user, err := u.GetByID(ctx, id)
		if err != nil {
			return false, err
		}
		return user.CheckPassword(password), nil
	})
}
