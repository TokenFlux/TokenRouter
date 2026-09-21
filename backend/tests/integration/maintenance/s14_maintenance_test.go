//go:build integration

package maintenance_test

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"

	"github.com/TokenFlux/TokenRouter/internal/backup"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"

	idempotencypg "github.com/TokenFlux/TokenRouter/internal/idempotency/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
	"github.com/TokenFlux/TokenRouter/migrations"
	_ "github.com/lib/pq"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// 系统锁只使用测试框架的隔离数据库，不接触生产数据。
func TestS14B05SystemLockOwnership(t *testing.T) {
	ctx := context.Background()
	integrationDB := maintenanceDatabase(t)
	repo := idempotencypg.NewIdempotencyRepository(integrationDB)
	operationStore := idempotencypg.NewOperationLeaseStore(integrationDB)
	for _, scenario := range []string{"reclaim-renew", "late-release"} {
		t.Run(scenario, func(t *testing.T) {
			_, err := integrationDB.ExecContext(ctx, "DELETE FROM idempotency_records WHERE scope=$1", "admin.system.operations.global_lock")
			if err != nil {
				t.Fatal(err)
			}
			svc := maintenance.NewSystemOperationLockService(operationStore, maintenance.Options{Log: logging.LegacyPrintf, ProcessingTimeout: time.Hour, SystemOperationTTL: 2 * time.Hour})
			first, err := svc.Acquire(ctx, "s14-first")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = svc.Release(ctx, first, false, "cleanup") }()
			_, err = integrationDB.ExecContext(ctx, "UPDATE idempotency_records SET locked_until=NOW()-INTERVAL '1 second' WHERE scope=$1", "admin.system.operations.global_lock")
			if err != nil {
				t.Fatal(err)
			}
			second, err := svc.Acquire(ctx, "s14-second")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = svc.Release(ctx, second, true, "") }()
			hash := idempotency.HashIdempotencyKey("global-system-operation-lock")
			current, err := repo.GetByScopeAndKeyHash(ctx, "admin.system.operations.global_lock", hash)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "reclaim-renew" {
				ok, err := operationStore.RenewOperation(ctx, current.ID, second.OperationID(), *current.ResponseBody, time.Now().Add(time.Hour), time.Now().Add(2*time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					t.Errorf("新持有者不能续租；数据库指纹仍为 %s", current.RequestFingerprint)
				}
			} else {
				if err := svc.Release(ctx, first, true, ""); err == nil {
					t.Fatal("旧持有者释放必须报告丢失所有权")
				}
				current, err = repo.GetByScopeAndKeyHash(ctx, "admin.system.operations.global_lock", hash)
				if err != nil {
					t.Fatal(err)
				}
				if current.Status != idempotency.IdempotencyStatusProcessing {
					t.Errorf("旧持有者释放覆盖新锁: %s", current.Status)
				}
			}
		})
	}
}

// 用独立 PostgreSQL 与本机 psql 检查真实进程退出码，不依赖模拟返回。
func TestS14B04RestoreSQLFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("s14_restore"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pg.Terminate(context.Background()) }()
	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, "CREATE TABLE fixture(id INT PRIMARY KEY); INSERT INTO fixture VALUES(1)")
	if err != nil {
		t.Fatal(err)
	}
	dumper := bp.NewPgDumper(bp.DatabaseOptions{Host: host, Port: port.Int(), User: "postgres", Password: "postgres", DBName: "s14_restore", SSLMode: "disable"})
	err = dumper.Restore(ctx, strings.NewReader("DELETE FROM fixture; INSERT INTO definitely_missing_table VALUES(1); INSERT INTO fixture VALUES(2);"))
	var count int
	if e := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture WHERE id=1").Scan(&count); e != nil {
		t.Fatal(e)
	}
	t.Logf("原行保留=%d，Restore error=%v", count, err)
	if err == nil {
		t.Error("SQL 失败却报告恢复成功")
	}
	if count != 1 {
		t.Error("失败恢复未保持原数据库")
	}
	// 成功路径确实提交，随后取消恢复必须保留这个已提交状态。
	err = dumper.Restore(ctx, strings.NewReader("DELETE FROM fixture; INSERT INTO fixture VALUES(2);"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture WHERE id=2").Scan(&count); err != nil || count != 1 {
		t.Fatalf("成功恢复未提交: count=%d error=%v", count, err)
	}
	cancelled, cancelRestore := context.WithTimeout(ctx, 80*time.Millisecond)
	err = dumper.Restore(cancelled, strings.NewReader("DELETE FROM fixture; SELECT pg_sleep(5); INSERT INTO fixture VALUES(3);"))
	cancelRestore()
	if err == nil {
		t.Fatal("取消恢复不能报告成功")
	}
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture WHERE id=2").Scan(&count); err != nil || count != 1 {
		t.Fatalf("取消恢复未回滚: count=%d error=%v", count, err)
	}
	// 输入读取失败在关闭标准输入前取消 psql，不能以 EOF 代替完整归档。
	err = dumper.Restore(ctx, &s14BrokenSQLReader{})
	if err == nil {
		t.Fatal("损坏输入不能报告成功")
	}
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture WHERE id=2").Scan(&count); err != nil || count != 1 {
		t.Fatalf("输入失败未回滚: count=%d error=%v", count, err)
	}

	// 使用真实 pg_dump、gzip、本地归档与 psql 完成一轮备份恢复演练。
	path := t.TempDir()
	settings := &s14BackupSettings{values: map[string]string{}}
	core := backup.New(settings, backup.Options{DatabaseName: "s14_restore", LocalPath: path, Now: time.Now}, nil, nil, bp.NewLocalBackupStore(path), bp.NewArchive(dumper, 0), func(ctx context.Context) (func(), bool, error) {
		return postgresinfra.TryAcquireDBAdvisoryLockWithError(ctx, db, postgresinfra.HashAdvisoryLockID("maintenance:database-heavy"))
	})
	defer core.Stop()
	record, err := core.CreateBackup(ctx, "manual", 1)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "completed" {
		t.Fatalf("备份未完成: %s", record.Status)
	}
	if _, err = db.ExecContext(ctx, "UPDATE fixture SET id=9"); err != nil {
		t.Fatal(err)
	}
	if err = core.RestoreBackup(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture WHERE id=2").Scan(&count); err != nil || count != 1 {
		t.Fatalf("归档恢复内容错误: count=%d error=%v", count, err)
	}

}

// s14BrokenSQLReader 在有效 SQL 后返回读取错误，模拟 gzip 尾部损坏。
type s14BrokenSQLReader struct{ sent bool }

func (r *s14BrokenSQLReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, "DELETE FROM fixture; INSERT INTO fixture VALUES(4);"), nil
}

// 测试设置仅隔离归档元数据，归档数据始终来自真实 PostgreSQL。
type s14BackupSettings struct{ values map[string]string }

func (s *s14BackupSettings) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *s14BackupSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

// maintenanceDatabase 只创建本测试的隔离数据库，使用已发布迁移验证原表与维护锁。
func maintenanceDatabase(t *testing.T) *sql.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("s16_maintenance"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := pg.Terminate(cleanup); err != nil {
			t.Error(err)
		}
	})
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := postgresinfra.ApplyMigrations(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	return db
}
