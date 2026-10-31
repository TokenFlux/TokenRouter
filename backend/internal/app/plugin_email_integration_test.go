//go:build integration

package app

import (
	"context"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	mailtest "github.com/TokenFlux/TokenRouter/internal/notification/testkit"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	pluginpostgres "github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// pluginEmailCapture records the resolved destination and localized notification content.
type pluginEmailCapture struct {
	address string // resolved account mailbox address
	subject string // rendered plugin notification subject
	calls   int    // external transport invocation count
}

// SendEmail captures one sequential message without contacting an external SMTP server.
func (s *pluginEmailCapture) SendEmail(_ context.Context, address, subject, _ string) error {
	s.address = address
	s.subject = subject
	s.calls++
	return nil
}

// TestPluginEmailResolvesMailboxLocaleAndBoundVersion reads real account records through the production notification adapter.
// Reserved addresses skip sending, recipient language localizes the correct process version, and an unrelated version cannot borrow current metadata.
func TestPluginEmailResolvesMailboxLocaleAndBoundVersion(t *testing.T) {
	db := postgrescontainer.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	account, err := client.User.Create().SetEmail("plugin-mail@example.invalid").SetPasswordHash("fixture").SetUsername("Reader").Save(t.Context())
	require.NoError(t, err)
	reserved, err := client.User.Create().SetEmail("subject" + identity.OIDCConnectSyntheticEmailDomain).SetPasswordHash("fixture").Save(t.Context())
	require.NoError(t, err)
	store := pluginpostgres.New(client)
	item := &plugin.Installation{PluginID: "test.email", Enabled: true, VersionSnapshot: plugin.VersionSnapshot{
		Version: "2.0.0", Source: plugin.SourceUpload, ManifestSHA256: strings.Repeat("a", 64),
		Manifest: &manifest.Manifest{ID: "test.email", Version: "2.0.0", Name: manifest.LocalizedText{"en": "New name", "zh-Hans": "新插件名"}},
	}}
	previous := item.VersionSnapshot
	previous.Version = "1.0.0"
	previous.Manifest = &manifest.Manifest{ID: item.PluginID, Version: "1.0.0", Name: manifest.LocalizedText{"en": "Old name", "zh-Hans": "原插件名"}}
	item.Previous = &previous
	require.NoError(t, store.Save(t.Context(), item))
	capture := &pluginEmailCapture{}
	notifications := notification.NewNotificationEmailService(mailtest.NewMemorySettings(), capture)
	notifications.SetRecipientLocaleReader(func(context.Context, int64, string) string { return "zh-Hans" })
	sender := &pluginEmailSender{
		users:         identity.NewUserService(identitypostgres.NewUserStore(client, db), nil, nil, nil, nil),
		installations: plugin.NewDevSource(plugin.DevOptions{}, nil, store, nil, nil),
		notifications: notifications,
	}
	binding := plugin.Binding{PluginID: item.PluginID, Version: "1.0.0"}
	input := plugin.EmailInput{UserID: reserved.ID, Subject: "Reward", BodyText: "Hello", Key: "reserved"}
	result, err := sender.Deliver(t.Context(), binding, input)
	require.NoError(t, err)
	require.Equal(t, plugin.EmailDelivery{}, result)
	require.Zero(t, capture.calls)
	input.UserID = account.ID
	input.Key = "old-version"
	result, err = sender.Deliver(t.Context(), binding, input)
	require.NoError(t, err)
	require.True(t, result.Delivered)
	require.Equal(t, account.Email, capture.address)
	require.Equal(t, "原插件名: Reward", capture.subject)
	binding.Version = "2.0.0"
	input.Key = "new-version"
	_, err = sender.Deliver(t.Context(), binding, input)
	require.NoError(t, err)
	require.Equal(t, "新插件名: Reward", capture.subject)
	binding.Version = "0.9.0"
	_, err = sender.Deliver(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrHostUnavailable)
	require.Equal(t, 2, capture.calls)
	input.UserID = 999999
	_, err = sender.Deliver(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrHostUserNotFound)
}
