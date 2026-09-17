package app

import (
	"context"
	"database/sql"
	"time"

	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/backup"
	bh "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	ip "github.com/TokenFlux/TokenRouter/internal/idempotency/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	pg "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	oh "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provideBackup 绑定唯一运行实例，维护取消早于 HTTP 等待。
func provideBackup(settings *settings.Store, cfg *config.Config, cipher identity.SecretEncryptor, db *sql.DB, manager *lifecycle.Manager) *backup.BackupService {
	d := cfg.Database
	path := bp.DefaultBackupLocalPath()
	dumper := bp.NewPgDumper(bp.DatabaseOptions{Host: d.Host, Port: d.Port, User: d.User, Password: d.Password, DBName: d.DBName, SSLMode: d.SSLMode})
	lock := func(ctx context.Context) (func(), bool, error) {
		if db == nil {
			return func() {}, true, nil
		}
		return pg.TryAcquireDBAdvisoryLockWithError(ctx, db, pg.HashAdvisoryLockID("maintenance:database-heavy"))
	}
	options := backup.Options{
		Log:                     logging.LegacyPrintf,
		DatabaseName:            d.DBName,
		LocalPath:               path,
		EncryptionKeyConfigured: cfg.Totp.EncryptionKeyConfigured,
		Now:                     time.Now,
	}
	core := backup.New(settings, options, cipher, bp.NewS3BackupStoreFactory(), bp.NewLocalBackupStore(path), bp.NewArchive(dumper, 0), lock)
	manager.Register(lifecycle.Hook{Name: "BackupAdmission", StartOrder: 980, StopOrder: 14, Stop: func(ctx context.Context) error { core.BeginStopContext(ctx); return nil }})
	manager.Register(lifecycle.Hook{Name: "BackupService", StartOrder: 980, StopOrder: 20, Start: core.StartContext, Stop: core.StopContext})
	return core
}

func provideBackupHTTP(core *backup.BackupService, users *identity.UserService) *bh.BackupHandler {
	return bh.NewBackupHandler(core, func(ctx context.Context, id int64, password string) (bool, error) {
		user, err := users.GetByID(ctx, id)
		if err != nil {
			return false, err
		}
		return user.CheckPassword(password), nil
	})
}
func provideDataManagementHTTP() *bh.DataManagementHandler {
	return bh.NewDataManagementHandler(backup.NewDataManagementService())
}
func provideSystemLock(db *sql.DB, cfg *config.Config) *maintenance.SystemOperationLockService {
	store := ip.NewOperationLeaseStore(db)
	return maintenance.NewSystemOperationLockService(store, maintenance.Options{
		Log:                logging.LegacyPrintf,
		ProcessingTimeout:  time.Duration(cfg.Idempotency.ProcessingTimeoutSeconds) * time.Second,
		SystemOperationTTL: time.Duration(cfg.Idempotency.SystemOperationTTLSeconds) * time.Second,
	})
}
func provideSystemOperations(update *maintenance.UpdateService, lock *maintenance.SystemOperationLockService, restart *lifecycle.Restarter, manager *lifecycle.Manager) *maintenance.Operations {
	core := maintenance.NewOperations(update, lock, restart)
	manager.Register(lifecycle.Hook{Name: "SystemMaintenanceAdmission", StartOrder: 980, StopOrder: 14, Stop: func(ctx context.Context) error { core.BeginStop(); return nil }})
	manager.Register(lifecycle.Hook{Name: "SystemMaintenanceOperations", StartOrder: 980, StopOrder: 17, Stop: core.StopContext})
	return core
}
func provideSystemHTTP(update *maintenance.UpdateService, core *maintenance.Operations) *oh.SystemHandler {
	return oh.NewSystemRuntimeHandler(update, core)
}
