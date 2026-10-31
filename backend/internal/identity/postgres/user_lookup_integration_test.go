//go:build integration

package postgres

import (
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// TestExactUserLookupCannotMatchNotesOrKeys seeds data that the administrative fuzzy search would match.
// Exact username and email criteria ignore those fields, preserve literal wildcard characters, and exclude deleted accounts even from ID batches.
func TestExactUserLookupCannotMatchNotesOrKeys(t *testing.T) {
	db := postgrescontainer.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := NewUserStore(client, db)
	service := identity.NewUserService(store, nil, nil, nil, nil)
	var live, deleted int64
	require.NoError(t, db.QueryRowContext(t.Context(), `INSERT INTO users(email,password_hash,username,notes,role,status) VALUES('Lookup@Example.Invalid','hash','ExactName','private-note-fragment','user','active') RETURNING id`).Scan(&live))
	require.NoError(t, db.QueryRowContext(t.Context(), `INSERT INTO users(email,password_hash,username,role,status,deleted_at) VALUES('deleted@example.invalid','hash','DeletedName','user','active',$1) RETURNING id`, time.Now()).Scan(&deleted))
	_, err := db.ExecContext(t.Context(), `INSERT INTO api_keys(user_id,key,name) VALUES($1,'sk-key-fragment','fixture')`, live)
	require.NoError(t, err)
	for _, name := range []string{"private-note-fragment", "key-fragment", "Exact", "%", "exactname"} {
		records, err := service.LookupUsersExact(t.Context(), identity.UserLookup{Username: name})
		require.NoError(t, err)
		require.Empty(t, records, name)
	}
	records, err := service.LookupUsersExact(t.Context(), identity.UserLookup{Username: "ExactName"})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, live, records[0].ID)
	records, err = service.LookupUsersExact(t.Context(), identity.UserLookup{Email: "lookup@example.invalid"})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "Lookup@Example.Invalid", records[0].Email)
	records, err = service.LookupUsersExact(t.Context(), identity.UserLookup{Email: "%@example.invalid"})
	require.NoError(t, err)
	require.Empty(t, records)
	records, err = service.LookupUsersExact(t.Context(), identity.UserLookup{IDs: []int64{live, deleted}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, live, records[0].ID)
}
