package account

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type createSnapshotStore struct {
	AdminStore
	privacyWrites int
}

func (s *createSnapshotStore) Create(_ context.Context, value *Record) error {
	value.ID = 1
	return nil
}
func (s *createSnapshotStore) UpdatePrivacyModeIfUnchanged(context.Context, UsageObservationVersion, string) (bool, error) {
	s.privacyWrites++
	return true, nil
}

// 创建后的受跟踪任务不得修改已经返回给 HTTP 的账号值。
func TestCreatePrivacyTaskHasIndependentReturnSnapshot(t *testing.T) {
	store := &createSnapshotStore{}
	privacy := NewPrivacyService(store, nil, PrivacyOptions{Antigravity: func(context.Context, string, string, string) string { return AntigravityPrivacySet }})
	var task func()
	admin := NewAdmin(store, AdminOptions{
		Creation:    CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: func() string { return "00000000-0000-4000-8000-000000000001" }},
		Credentials: CreateCredentialHooks{Validate: func(context.Context, *Record) error { return nil }},
		Privacy:     privacy, Background: func(_ string, fn func()) bool { task = fn; return true }, Error: func(string, ...any) {},
	})
	result, err := admin.CreateAccount(context.Background(), &CreateAccountInput{Name: "snapshot", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture"}})
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Empty(t, result.Extra)
	task()
	require.Equal(t, 1, store.privacyWrites)
	require.Empty(t, result.Extra, "后置任务不能补写返回快照的 map")
}
