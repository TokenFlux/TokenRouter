package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// userQueryFixture retains profile data so masking can be checked for aliasing.
type userQueryFixture struct {
	account  AccountView // unfiltered host account observation
	profiles []UserView  // reader-owned profile observation slice
	calls    int         // admitted domain read count
}

// usageQueryFixture observes normalized range and scope arguments without database access.
type usageQueryFixture struct {
	calls  int     // admitted usage query count
	ids    []int64 // normalized actor identifier batch
	limit  int     // normalized ranking row count
	metric string  // normalized ranking metric name
}

// Account returns the retained host observation without changing its source fields.
func (f *userQueryFixture) Account(context.Context, int64) (AccountView, error) {
	f.calls++
	return f.account, nil
}

// Lookup returns the retained profile slice to expose accidental in-place masking.
func (f *userQueryFixture) Lookup(context.Context, UserLookup) ([]UserView, error) {
	f.calls++
	return f.profiles, nil
}

// Identities returns no bindings while recording admitted identity-read requests.
func (f *userQueryFixture) Identities(context.Context, int64) ([]IdentityView, error) {
	f.calls++
	return nil, nil
}

// Summary records one authorized account summary request and returns empty counters.
func (f *usageQueryFixture) Summary(context.Context, int64) (UsageSummaryView, error) {
	f.calls++
	return UsageSummaryView{}, nil
}

// Stats retains the normalized actor batch received from the query boundary.
func (f *usageQueryFixture) Stats(_ context.Context, ids []int64, _, _ time.Time) ([]UserUsageView, error) {
	f.calls++
	f.ids = ids
	return nil, nil
}

// Ranking records the bounded row count and validated fixed metric name.
func (f *usageQueryFixture) Ranking(_ context.Context, _, _ time.Time, limit int, metric string) ([]RankingView, error) {
	f.calls++
	f.limit = limit
	f.metric = metric
	return nil, nil
}

// TestReadQueriesSeparateProfileEmailAndFunding checks both profile entry points against a retained host-side email value.
// Missing email permission masks only returned copies, and exact-email selection cannot reach storage without its additional capability.
func TestReadQueriesSeparateProfileEmailAndFunding(t *testing.T) {
	source := &userQueryFixture{
		account:  AccountView{User: UserView{ID: 7, Email: "private@example.invalid"}, Wallet: WalletView{Balance: "3", TotalRecharged: "9"}},
		profiles: []UserView{{ID: 7, Email: "private@example.invalid"}},
	}
	query := NewQueries(QueryReaders{Users: source}, nil)
	binding := Binding{PluginID: "test.queries", Permissions: []string{"user.read", "user.lookup"}}
	user, err := query.User(t.Context(), binding, 7)
	require.NoError(t, err)
	require.Empty(t, user.Email)
	require.Equal(t, "private@example.invalid", source.account.User.Email)
	profiles, err := query.LookupUsers(t.Context(), binding, UserLookup{IDs: []int64{7}})
	require.NoError(t, err)
	require.Empty(t, profiles[0].Email)
	require.Equal(t, "private@example.invalid", source.profiles[0].Email)
	before := source.calls
	_, err = query.LookupUsers(t.Context(), binding, UserLookup{Email: "private@example.invalid"})
	require.ErrorIs(t, err, ErrPermissionNotGranted)
	require.Equal(t, before, source.calls)
	_, err = query.Wallet(t.Context(), binding, 7)
	require.ErrorIs(t, err, ErrPermissionNotGranted)
	binding.Permissions = append(binding.Permissions, "user.email", "wallet.read")
	user, err = query.User(t.Context(), binding, 7)
	require.NoError(t, err)
	require.Equal(t, "private@example.invalid", user.Email)
	wallet, err := query.Wallet(t.Context(), binding, 7)
	require.NoError(t, err)
	require.Equal(t, "9", wallet.TotalRecharged)
}

// TestUsageQueriesEnforceCallerScopeAndBoundedInputs contrasts caller-only usage with broad reporting authority.
// Wrong-user grants and oversized intervals never reach readers, while valid batches are normalized without mutating caller slices.
func TestUsageQueriesEnforceCallerScopeAndBoundedInputs(t *testing.T) {
	grants, err := NewGrants("query-grant-fixture", nil)
	require.NoError(t, err)
	source := &usageQueryFixture{}
	query := NewQueries(QueryReaders{Usage: source}, grants)
	binding := Binding{PluginID: "test.queries", Permissions: []string{"usage.self"}}
	token, err := grants.Issue(binding.PluginID, 7)
	require.NoError(t, err)
	_, err = query.UsageSummary(t.Context(), binding, 8, token)
	require.ErrorIs(t, err, ErrGrantInvalid)
	require.Zero(t, source.calls)
	_, err = query.UsageSummary(t.Context(), binding, 7, token)
	require.NoError(t, err)
	binding.Permissions = []string{"usage.read"}
	_, err = query.UsageSummary(t.Context(), binding, 8, "not-a-grant")
	require.NoError(t, err)
	ids := []int64{8, 7, 8}
	_, err = query.UsageStats(t.Context(), binding, ids, 1800000000, 1800000100)
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, source.ids)
	require.Equal(t, []int64{8, 7, 8}, ids)
	before := source.calls
	_, err = query.UsageStats(t.Context(), binding, ids, 1800000000, 1800000000+94*24*60*60)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Equal(t, before, source.calls)
	_, err = query.UsageRanking(t.Context(), binding, 1800000000, 1800000100, 0, "")
	require.NoError(t, err)
	require.Equal(t, 20, source.limit)
	require.Equal(t, "tokens", source.metric)
	_, err = query.UsageRanking(t.Context(), binding, 1800000000, 1800000100, 101, "cost")
	require.ErrorIs(t, err, ErrInvalidArgument)
}
