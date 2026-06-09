package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAccountRepositoryListGroupCapacityAccountsUsesLightweightBatchQuery(t *testing.T) {
	var capturedSQL string
	matcher := sqlmock.QueryMatcherFunc(func(expectedSQL, actualSQL string) error {
		capturedSQL = actualSQL
		compact := strings.ToLower(strings.Join(strings.Fields(actualSQL), " "))
		if !strings.Contains(compact, "from account_groups ag") {
			return fmt.Errorf("expected account_groups query, got %s", compact)
		}
		if !strings.Contains(compact, "join accounts a on a.id = ag.account_id") {
			return fmt.Errorf("expected account join, got %s", compact)
		}
		if !strings.Contains(compact, "ag.group_id = any($1)") {
			return fmt.Errorf("expected batched group_id ANY predicate, got %s", compact)
		}
		for _, forbidden := range []string{"credentials", "withaccount", "proxy_id", "select *"} {
			if strings.Contains(compact, forbidden) {
				return fmt.Errorf("query should stay lightweight and not include %q: %s", forbidden, compact)
			}
		}
		return nil
	})

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectQuery("SELECT lightweight group capacity accounts").
		WithArgs(pq.Array([]int64{10, 20})).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "id", "concurrency", "max_sessions", "session_idle_timeout_minutes", "base_rpm"}).
			AddRow(int64(10), int64(1), 3, 2, 7, 100).
			AddRow(int64(10), int64(2), 4, 0, 5, 0).
			AddRow(int64(20), int64(3), 5, 1, 5, 60))

	got, err := repo.ListGroupCapacityAccounts(context.Background(), []int64{10, 20, 10, 0})
	require.NoError(t, err)
	require.Len(t, got[10], 2)
	require.Len(t, got[20], 1)
	require.Equal(t, int64(1), got[10][0].ID)
	require.Equal(t, 7, got[10][0].SessionIdleTimeoutMinutes)
	require.Equal(t, 100, got[10][0].BaseRPM)
	require.NotEmpty(t, capturedSQL)
	require.NoError(t, mock.ExpectationsWereMet())
}
