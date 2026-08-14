package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func invitationPreviewRows(expiresAt time.Time, email string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"email",
		"status",
		"expires_at",
		"team_name",
		"inviter_name",
		"inviter_email",
	}).AddRow(
		int64(17),
		email,
		"pending",
		expiresAt,
		"词元流动",
		"喵窝",
		"owner@example.com",
	)
}

func TestTeamRepositoryPreviewInvitationReturnsVerifiedSummary(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &teamRepository{db: db}
	now := time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	mock.ExpectQuery("SELECT ti.id, ti.email, ti.status, ti.expires_at, t.name").
		WithArgs("token-hash").
		WillReturnRows(invitationPreviewRows(expiresAt, "member@example.com"))

	preview, err := repo.PreviewInvitation(context.Background(), "token-hash", "member@example.com", now)

	require.NoError(t, err)
	require.Equal(t, "词元流动", preview.TeamName)
	require.Equal(t, "喵窝", preview.InviterName)
	require.Equal(t, "owner@example.com", preview.InviterEmail)
	require.Equal(t, expiresAt, preview.ExpiresAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTeamRepositoryPreviewInvitationRejectsDifferentEmail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &teamRepository{db: db}
	now := time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT ti.id, ti.email, ti.status, ti.expires_at, t.name").
		WithArgs("token-hash").
		WillReturnRows(invitationPreviewRows(now.Add(time.Hour), "other@example.com"))

	preview, err := repo.PreviewInvitation(context.Background(), "token-hash", "member@example.com", now)

	require.ErrorIs(t, err, service.ErrTeamInvitationEmail)
	require.Nil(t, preview)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTeamRepositoryPreviewInvitationMarksExpiredInvitation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &teamRepository{db: db}
	now := time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT ti.id, ti.email, ti.status, ti.expires_at, t.name").
		WithArgs("token-hash").
		WillReturnRows(invitationPreviewRows(now.Add(-time.Minute), "member@example.com"))
	mock.ExpectExec("UPDATE team_invitations SET status = 'expired'").
		WithArgs(int64(17), now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	preview, err := repo.PreviewInvitation(context.Background(), "token-hash", "member@example.com", now)

	require.ErrorIs(t, err, service.ErrTeamInvitationExpired)
	require.Nil(t, preview)
	require.NoError(t, mock.ExpectationsWereMet())
}
