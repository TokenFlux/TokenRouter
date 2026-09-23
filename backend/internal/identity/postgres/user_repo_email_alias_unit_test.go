package postgres_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/stretchr/testify/require"
)

func seedAliasUser(t *testing.T, repo *postgres.UserStore, email string) *identity.User {
	t.Helper()
	user := &identity.User{
		Email:        email,
		Username:     email,
		PasswordHash: "hash",
		Role:         identity.RoleUser,
		Status:       identity.StatusActive,
	}
	require.NoError(t, repo.Create(context.Background(), user))
	return user
}

func TestUserRepositoryExistsByEmailAlias(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		probe  string
		want   bool
	}{
		{name: "same address", stored: "someone@gmail.com", probe: "someone@gmail.com", want: true},
		{name: "gmail plus alias", stored: "someone@gmail.com", probe: "someone+bulk@gmail.com", want: true},
		{name: "gmail dot alias", stored: "d.axis.2026@gmail.com", probe: "daxis2026@gmail.com", want: true},
		{name: "googlemail alias", stored: "someone@googlemail.com", probe: "some.one@gmail.com", want: true},
		{name: "root dot", stored: "d.axis.2026@gmail.com.", probe: "daxis2026@gmail.com", want: true},
		{name: "legacy spacing", stored: "  D.Axis.2026@Gmail.com  ", probe: "daxis2026@gmail.com", want: true},
		{name: "non gmail plus alias", stored: "first.last@qq.com", probe: "first.last+tag@qq.com", want: true},
		{name: "different inbox", stored: "someone@gmail.com", probe: "someoneelse@gmail.com", want: false},
		{name: "non gmail dots significant", stored: "first.last@qq.com", probe: "firstlast@qq.com", want: false},
		{name: "different domain", stored: "someone@gmail.com", probe: "someone@qq.com", want: false},
		{name: "leading plus locals distinct", stored: "+alice@gmail.com", probe: "+bob@gmail.com", want: false},
		{name: "underscore is literal", stored: "user_x@qq.com", probe: "userax@qq.com", want: false},
		{name: "percent is literal", stored: "a%b@qq.com", probe: "axxb@qq.com", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := newUserEntRepo(t)
			seedAliasUser(t, repo, tc.stored)

			got, err := repo.ExistsByEmailAlias(context.Background(), tc.probe)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestUserRepositoryEmailAliasOwnerPrefersOtherUser(t *testing.T) {
	repo, _ := newUserEntRepo(t)
	ctx := context.Background()
	self := seedAliasUser(t, repo, "inbox+own@gmail.com")
	other := seedAliasUser(t, repo, "inbox+legacy@gmail.com")

	ownerID, exists, err := repo.EmailAliasOwnerID(ctx, "inbox+new@gmail.com", self.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, other.ID, ownerID)
}

func TestUserRepositoryEmailAliasIgnoresMalformedInput(t *testing.T) {
	repo, _ := newUserEntRepo(t)
	seedAliasUser(t, repo, "someone@gmail.com")

	got, err := repo.ExistsByEmailAlias(context.Background(), "not-an-email")
	require.NoError(t, err)
	require.False(t, got)
}
