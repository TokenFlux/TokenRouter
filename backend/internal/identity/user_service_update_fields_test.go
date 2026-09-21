//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

// 这些用例锁死"每个入口只声明自己真正要改的列"：
// 任何退回整行回写的改动都会让并发写入被陈旧快照覆盖，并在这里变红。

func TestUpdateProfile_OnlyDeclaresRequestedColumns(t *testing.T) {
	username := "renamed"
	tests := []struct {
		name string
		req  identity.UpdateProfileRequest
		want identity.UserUpdateFields
	}{
		{
			name: "username only",
			req:  identity.UpdateProfileRequest{Username: &username},
			want: identity.UserUpdateFields{Username: true},
		},
		{
			name: "notify settings only",
			req:  identity.UpdateProfileRequest{BalanceNotifyEnabled: boolPtr(true)},
			want: identity.UserUpdateFields{BalanceNotifySettings: true},
		},
		{
			name: "username and notify threshold",
			req:  identity.UpdateProfileRequest{Username: &username, BalanceNotifyThreshold: float64Ptr(1.5)},
			want: identity.UserUpdateFields{Username: true, BalanceNotifySettings: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockUserRepo{getByIDUser: &identity.User{ID: 7, Balance: 0.30, Status: identity.StatusActive}}
			svc := identity.NewUserService(repo, nil, nil, nil, runProfileBackground)

			_, err := svc.UpdateProfile(context.Background(), 7, tt.req)
			require.NoError(t, err)
			require.Equal(t, []identity.UserUpdateFields{tt.want}, repo.updateFields)
		})
	}
}

// 只改头像时用户行没有任何列要写，不应产生一次整行更新。
func TestUpdateProfile_AvatarOnlySkipsUserRowWrite(t *testing.T) {
	repo := &mockUserRepo{getByIDUser: &identity.User{ID: 7, Balance: 0.30}}
	svc := identity.NewUserService(repo, nil, nil, nil, runProfileBackground)

	avatar := "https://cdn.example.com/a.png"
	_, err := svc.UpdateProfile(context.Background(), 7, identity.UpdateProfileRequest{AvatarURL: &avatar})
	require.NoError(t, err)
	require.Len(t, repo.upsertAvatarArgs, 1, "avatar must still be stored")
	require.Equal(t, []identity.UserUpdateFields{{}}, repo.updateFields, "no user column should be declared")
}

func TestChangePassword_OnlyDeclaresPasswordHash(t *testing.T) {
	user := &identity.User{ID: 7, Balance: 0.30}
	require.NoError(t, user.SetPassword("old-password"))
	repo := &mockUserRepo{getByIDUser: user}
	svc := identity.NewUserService(repo, nil, nil, nil, runProfileBackground)

	err := svc.ChangePassword(context.Background(), 7, identity.ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "new-password",
	})
	require.NoError(t, err)
	require.Equal(t, []identity.UserUpdateFields{{PasswordHash: true}}, repo.updateFields)
}

func TestUpdateStatus_OnlyDeclaresStatus(t *testing.T) {
	repo := &mockUserRepo{getByIDUser: &identity.User{ID: 7, Balance: 0.30, Status: identity.StatusActive}}
	svc := identity.NewUserService(repo, nil, nil, nil, runProfileBackground)

	require.NoError(t, svc.UpdateStatus(context.Background(), 7, "disabled"))
	require.Equal(t, []identity.UserUpdateFields{{Status: true}}, repo.updateFields)
}
