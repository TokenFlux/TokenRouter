//go:build integration

package identity_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
)

func (s *UserRepoSuite) TestCreate_CreatesEmailAuthIdentityForNormalEmail() {
	user := &identitycore.User{
		Email:        "repo-create@example.com",
		PasswordHash: "test-password-hash",
		Role:         identitycore.RoleUser,
		Status:       billing.StatusActive,
		Concurrency:  2,
	}

	s.Require().NoError(s.repo.Create(s.ctx, user))

	identity, err := s.client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("repo-create@example.com"),
		).
		Only(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(user.ID, identity.UserID)
}

func (s *UserRepoSuite) TestCreate_SkipsEmailAuthIdentityForSyntheticLinuxDoEmail() {
	user := &identitycore.User{
		Email:        "linuxdo-legacy-user@linuxdo-connect.invalid",
		PasswordHash: "test-password-hash",
		Role:         identitycore.RoleUser,
		Status:       billing.StatusActive,
		Concurrency:  2,
	}

	s.Require().NoError(s.repo.Create(s.ctx, user))

	count, err := s.client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
		).
		Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Zero(count)
}

func (s *UserRepoSuite) TestUpdate_ReplacesEmailAuthIdentityWhenEmailChanges() {
	user := s.mustCreateUser(&identitycore.User{
		Email: "before-update@example.com",
	})

	user.Email = "after-update@example.com"
	s.Require().NoError(s.repo.Update(s.ctx, user, identitycore.UserUpdateFields{Email: true}))

	newIdentity, err := s.client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("after-update@example.com"),
		).
		Only(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(user.ID, newIdentity.UserID)

	oldCount, err := s.client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("before-update@example.com"),
		).
		Count(context.Background())
	s.Require().NoError(err)
	s.Require().Zero(oldCount)
}
