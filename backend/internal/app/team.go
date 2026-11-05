package app

import (
	"context"
	"database/sql"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	teampostgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// team.go assembles team persistence, identity lookups, and notification
// dependencies.
// Team persistence shares SQL participants for membership, Key lifecycle, and
// member usage reporting. Identity users are narrowed to invitation/ownership
// snapshots, and the service receives notification delivery plus optional
// published Key invalidation and display settings from the application graph.

// teamIdentityUsers narrows identity records for team ownership and invitation
// consumers.
type teamIdentityUsers struct {
	Users identity.UserRepository // shared identity reads
}

// provideTeamRepository joins team persistence, Key participants, usage
// counters, and calendar reporting.
func provideTeamRepository(db *sql.DB, calendar timezone.Calendar, reports *usagepostgres.Store) team.TeamRepository {
	return teampostgres.NewTeamRepositoryWithReports(db, keypostgres.NewTeamKeys(db), billingpostgres.NewMemberUsageStore(db, &calendar), calendar, reports)
}

// GetByID returns identity fields needed by team operations for a stored user.
// Absent users remain nil, and available snapshots retain the repository error
// with ID, email, and username.
func (p teamIdentityUsers) GetByID(ctx context.Context, id int64) (*team.UserSnapshot, error) {
	u, e := p.Users.GetByID(ctx, id)
	if u == nil {
		return nil, e
	}
	return &team.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username}, e
}

// GetByEmail returns team identity data from the supplied email lookup. Missing
// users remain nil, and available snapshots retain the identity repository's
// error for invitation or ownership handling.
func (p teamIdentityUsers) GetByEmail(ctx context.Context, email string) (*team.UserSnapshot, error) {
	u, e := p.Users.GetByEmail(ctx, email)
	if u == nil {
		return nil, e
	}
	return &team.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username}, e
}

// provideTeam constructs membership and invitation rules with shared storage
// and mail delivery. Optional cfg supplies deployment defaults, display
// settings remain an optional interface, and Key invalidation is exposed only
// when a cache is supplied.
func provideTeam(repo team.TeamRepository, users *identitypostgres.UserStore, email *notification.Mailer, cache apikey.APIKeyCache, limiter team.TeamInvitationLimiter, settings *site.DisplaySettings, cfg *config.Config) *team.TeamService {
	var options *team.Options
	if cfg != nil {
		options = &team.Options{Enabled: cfg.Team.Enabled, SelfServiceEnabled: cfg.Team.SelfServiceEnabled, DefaultMemberLimit: cfg.Team.DefaultMaxMembers, FrontendURL: cfg.Site.FrontendURL, Now: time.Now}
	}
	var settingReader team.Settings
	if settings != nil {
		settingReader = settings
	}
	var invalidator team.KeyInvalidator
	if cache != nil {
		invalidator = apikey.PublishedAuthCacheInvalidator{Cache: cache}
	}
	return team.NewTeamService(repo, teamIdentityUsers{Users: users}, team.NewEmailNotifications(email, teamIdentityUsers{Users: users}), invalidator, limiter, settingReader, options)
}
