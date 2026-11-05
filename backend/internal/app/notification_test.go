package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"
	"github.com/TokenFlux/TokenRouter/internal/notification/testkit"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// notification_test.go checks notification assembly, reminder readiness, and
// public unsubscribe addresses. Local SMTP captures show that stored site settings
// precede deployment defaults, while missing addresses retain relative links.
// Readiness fixtures distinguish absent transport configuration from storage errors.

// notificationFrontendSettings injects address-read failures without breaking SMTP settings reads.
type notificationFrontendSettings struct {
	*testkit.MemorySettings      // mutable notification settings
	failFrontend            bool // frontend read failure
}

// expirySettingsFixture supplies empty mail settings or a configured repository
// error.
type expirySettingsFixture struct {
	// Repository supplies unused methods; GetMultiple implements the settings
	// snapshot consumed by notification transport readiness in these two cases.
	settings.Repository
	err error // injected settings failure
}

// GetValue rejects the frontend key when failure injection is enabled.
// Other keys delegate to the memory repository so mail transport, signing,
// recipient preferences, and delivery markers remain available during the test.
func (s *notificationFrontendSettings) GetValue(ctx context.Context, key string) (string, error) {
	if key == site.SettingKeyFrontendURL && s.failFrontend {
		return "", errors.New("frontend setting read failed")
	}
	return s.MemorySettings.GetValue(ctx, key)
}

// TestProvideNotificationResolvesFrontendURL sends optional mail through production
// assembly and a local SMTP server. The rendered link follows stored/deployment
// precedence on every send, including read failures and subsequent setting edits.
func TestProvideNotificationResolvesFrontendURL(t *testing.T) {
	for _, tc := range []struct {
		name       string // named resolution scenario
		stored     string // saved frontend address
		deployment string // deployment frontend default
		failRead   bool   // injected lookup failure
		want       string // expected public base
	}{
		{name: "stored wins", stored: " https://console.example/ ", deployment: "https://deployment.example", want: "https://console.example"},
		{name: "deployment fallback", deployment: "https://deployment.example/portal/", want: "https://deployment.example/portal"},
		{name: "blank stored", stored: " \t ", deployment: "https://deployment.example", want: "https://deployment.example"},
		{name: "failed read", stored: "https://ignored.example", deployment: "https://deployment.example", failRead: true, want: "https://deployment.example"},
		{name: "relative fallback"},
		{name: "failed read without deployment", failRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			server := testkit.StartSMTPServer(t)
			repo := &notificationFrontendSettings{MemorySettings: testkit.NewMemorySettings(), failFrontend: tc.failRead}
			require.NoError(t, repo.SetMultiple(ctx, server.Settings()))
			require.NoError(t, repo.SetMultiple(ctx, map[string]string{"api_base_url": "https://api.example/v1", "frontend_url": tc.stored}))
			store := settings.New(repo)
			cfg := &config.Config{Site: config.SiteConfig{FrontendURL: tc.deployment}}
			mailer := provideMailer(store)
			service := provideNotification(store, mailer, nil, provideSiteDisplay(store, cfg))
			// Recipient lookup is outside this assembly scenario. Disable that reader
			// so the fixture can exercise production link wiring without a user database.
			service.SetRecipientLocaleReader(nil)
			input := notification.SendRequest{Event: notification.NotificationEmailEventBalanceLow, RecipientEmail: "reader@example.com"}
			require.NoError(t, service.Send(ctx, input))
			require.Contains(t, server.LastMessageBody(t), `href="`+tc.want+`/api/v1/settings/email-unsubscribe?token=`)
			repo.failFrontend = false
			require.NoError(t, repo.Set(ctx, "frontend_url", "https://updated.example/"))
			require.NoError(t, service.Send(ctx, input))
			require.Contains(t, server.LastMessageBody(t), `href="https://updated.example/api/v1/settings/email-unsubscribe?token=`)
		})
	}
}

// GetMultiple returns an empty settings snapshot with the configured fixture
// error.
func (r expirySettingsFixture) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, r.err
}

// TestExpiryNotificationReadinessBridge checks empty SMTP settings and a failed
// settings read through the reminder adapter. It expects billing's unconfigured
// transport sentinel for absence and the original repository error for failure.
func TestExpiryNotificationReadinessBridge(t *testing.T) {
	for _, failure := range []error{nil, errors.New("db down")} {
		repo := expirySettingsFixture{err: failure}
		mailer := notification.NewMailer(repo, smtp.New())
		sender := notification.NewNotificationEmailService(repo, mailer)
		mailer.SetNotificationEmailService(sender)
		adapter := expiryNotifications{Service: sender}
		err := adapter.Ready(context.Background())
		if failure == nil {
			require.ErrorIs(t, err, billing.ErrReminderTransportUnconfigured)
		} else {
			require.ErrorIs(t, err, failure)
		}
	}
}
