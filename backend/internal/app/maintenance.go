package app

import (
	"context"
	"database/sql"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/backup"
	bh "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	ip "github.com/TokenFlux/TokenRouter/internal/idempotency/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	pg "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	oh "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// maintenance.go assembles backup and system maintenance services and registers
// their admission and drain hooks. Backup and system maintenance stop admitting
// work before the application waits for HTTP handlers. Their later drain hooks
// retain shared database resources until accepted operations finish, while
// backups use a database-heavy advisory lock and configured archive stores.

// provideBackup constructs backup storage and registers early admission stop
// plus later drain. cfg and manager are required; a nil database makes the lock
// callback allow work with a no-op release, while a database uses the
// maintenance advisory identity.
func provideBackup(settings *settings.Store, cfg *config.Config, cipher identity.SecretEncryptor, db *sql.DB, manager *lifecycle.Manager) *backup.BackupService {
	d := cfg.Database
	path := bp.DefaultBackupLocalPath()
	dumper := bp.NewPgDumper(bp.DatabaseOptions{Host: d.Host, Port: d.Port, User: d.User, Password: d.Password, DBName: d.DBName, SSLMode: d.SSLMode})
	lock := func(ctx context.Context) (func(), bool, error) {
		// Without a database, this binding supplies unconditional local admission.
		// Callers still receive a release function so backup lock ownership has one callback shape.
		if db == nil {
			return func() {}, true, nil
		}
		return pg.TryAcquireDBAdvisoryLockWithError(ctx, db, pg.HashAdvisoryLockID("maintenance:database-heavy"))
	}
	options := backup.Options{
		Log:                     logging.LegacyPrintf,
		LocalPath:               path,
		EncryptionKeyConfigured: cfg.Security.SecretEncryptionKeyConfigured,
		Now:                     time.Now,
	}
	core := backup.New(settings, options, cipher, bp.NewS3BackupStoreFactory(), bp.NewLocalBackupStore(path), bp.NewArchive(dumper, 0), lock)
	// Begin stopping before HTTP drain so handlers cannot begin more backup
	// work during shutdown; the later service hook waits for already accepted operations.
	manager.Register(lifecycle.Hook{Name: "BackupAdmission", StartOrder: 980, StopOrder: 14, Stop: func(ctx context.Context) error { core.BeginStopContext(ctx); return nil }})
	manager.Register(lifecycle.Hook{Name: "BackupService", StartOrder: 980, StopOrder: shutdownStageProducers, Start: core.StartContext, Stop: core.StopContext})
	return core
}

// provideBackupHTTP binds backup operations and password verification against
// user profiles.
func provideBackupHTTP(core *backup.BackupService, users *identity.UserService) *bh.BackupHandler {
	return bh.NewBackupHandler(core, func(ctx context.Context, id int64, password string) (bool, error) {
		user, err := users.GetByID(ctx, id)
		if err != nil {
			return false, err
		}
		return user.CheckPassword(password), nil
	})
}

// provideDataManagementHTTP constructs the administration handler for backup
// data management.
func provideDataManagementHTTP() *bh.DataManagementHandler {
	return bh.NewDataManagementHandler(backup.NewDataManagementService())
}

// provideSystemLock binds persistent system-operation leases with configured
// time budgets. cfg is required, and processing timeout plus lease lifetime
// seconds become durations consumed by the operation lock service.
func provideSystemLock(db *sql.DB, cfg *config.Config) *maintenance.SystemOperationLockService {
	store := ip.NewOperationLeaseStore(db)
	return maintenance.NewSystemOperationLockService(store, maintenance.Options{
		Log: logging.LegacyPrintf,
		// Lock processing and expiry budgets convert configuration seconds to durations.
		ProcessingTimeout:  time.Duration(cfg.Idempotency.ProcessingTimeoutSeconds) * time.Second,
		SystemOperationTTL: time.Duration(cfg.Idempotency.SystemOperationTTLSeconds) * time.Second,
	})
}

// provideSystemOperations constructs updates, operation locking, and restart
// coordination before lifecycle startup. Admission stops at level fourteen and
// its work drains at level seventeen, keeping accepted maintenance ahead of
// later shared-storage cleanup.
func provideSystemOperations(update *maintenance.UpdateService, lock *maintenance.SystemOperationLockService, restart *lifecycle.Restarter, manager *lifecycle.Manager) *maintenance.Operations {
	core := maintenance.NewOperations(update, lock, restart)
	// Reject and cancel maintenance before HTTP waits, then drain its accepted
	// operations at the later hook while the lock and update dependencies remain usable.
	manager.Register(lifecycle.Hook{Name: "SystemMaintenanceAdmission", StartOrder: 980, StopOrder: 14, Stop: func(ctx context.Context) error { core.BeginStop(); return nil }})
	manager.Register(lifecycle.Hook{Name: "SystemMaintenanceOperations", StartOrder: 980, StopOrder: 17, Stop: core.StopContext})
	return core
}

// provideSystemHTTP combines release updates and maintenance operations for
// system administration.
func provideSystemHTTP(update *maintenance.UpdateService, core *maintenance.Operations) *oh.SystemHandler {
	return oh.NewSystemRuntimeHandler(update, core)
}
