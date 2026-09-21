package postgres

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDatabaseHeavyMaintenanceLockRejectsConcurrentTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(HashAdvisoryLockID("maintenance:database-heavy")).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

	release, acquired, err := TryAcquireDBAdvisoryLockWithError(context.Background(), db, HashAdvisoryLockID("maintenance:database-heavy"))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	if acquired || release != nil {
		t.Fatalf("acquired = %v release = %v, want busy", acquired, release != nil)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestDatabaseHeavyMaintenanceLockReleasesSession(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(HashAdvisoryLockID("maintenance:database-heavy")).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(HashAdvisoryLockID("maintenance:database-heavy")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	release, acquired, err := TryAcquireDBAdvisoryLockWithError(context.Background(), db, HashAdvisoryLockID("maintenance:database-heavy"))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	if !acquired || release == nil {
		t.Fatalf("acquired = %v release = %v, want acquired", acquired, release != nil)
	}
	release()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
