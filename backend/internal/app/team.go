// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"
	sql "database/sql"
	time "time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/site"
	team "github.com/TokenFlux/TokenRouter/internal/team"
	teampostgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
)

// provideTeamRepository 把成员状态、Key 生命周期与资金窗口参与能力装在同一 SQL 连接来源上。
func provideTeamRepository(db *sql.DB, calendar timezone.Calendar) team.TeamRepository {
	return teampostgres.NewTeamRepository(db, keypostgres.NewTeamKeys(db), billingpostgres.NewMemberUsageStore(db, &calendar), &calendar)
}

type teamIdentityUsers struct{ Users identity.UserRepository }

func (p teamIdentityUsers) GetByID(ctx context.Context, id int64) (*team.UserSnapshot, error) {
	u, e := p.Users.GetByID(ctx, id)
	if u == nil {
		return nil, e
	}
	return &team.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username}, e
}
func (p teamIdentityUsers) GetByEmail(ctx context.Context, email string) (*team.UserSnapshot, error) {
	u, e := p.Users.GetByEmail(ctx, email)
	if u == nil {
		return nil, e
	}
	return &team.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username}, e
}
func provideTeam(repo team.TeamRepository, users *identitypostgres.UserStore, email *notification.Mailer, cache apikey.APIKeyCache, limiter team.TeamInvitationLimiter, settings *site.DisplaySettings, cfg *config.Config) *team.TeamService {
	var options *team.Options
	if cfg != nil {
		options = &team.Options{Enabled: cfg.Team.Enabled, SelfServiceEnabled: cfg.Team.SelfServiceEnabled, DefaultMemberLimit: cfg.Team.DefaultMemberLimit, FrontendURL: cfg.Server.FrontendURL, Now: time.Now}
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
