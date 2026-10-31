package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
)

// plugin_email.go resolves account recipients and plugin presentation before
// handing plain-text notices to the shared notification service. Reserved provider
// mailbox placeholders count as no recipient. Recipient preferences determine
// language, while the bound process version selects current or previous manifest
// naming during an installation update that is draining an older generation.
//
// The plugin email coordinator owns durable quota claims and final results.
// This adapter reports whether the notification sender was invoked and whether
// transport acceptance is known, including acceptance followed by a marker error.
// Notification owns escaping, unsubscribe preferences, templates, and SMTP; the
// plugin never chooses a raw delivery address or receives mailbox credentials.

// pluginEmailSender adapts account and installation metadata to optional notification delivery.
type pluginEmailSender struct {
	users         *identity.UserService                  // current account recipient reader
	installations *plugin.DevSource                      // shared current and previous release source
	notifications *notification.NotificationEmailService // shared template and transport coordinator
}

// providePluginEmails binds durable claims to account-aware notification delivery.
// All collaborators remain owned by application lifecycle. The returned service
// starts no worker or SMTP connection until an authorized plugin requests delivery.
func providePluginEmails(writes *plugin.Writes, store *postgres.Store, source *plugin.DevSource, users *identity.UserService, notifications *notification.NotificationEmailService) *plugin.Emails {
	sender := &pluginEmailSender{users: users, installations: source, notifications: notifications}
	return plugin.NewEmails(writes, store, sender, time.Local, slog.Default())
}

// Deliver resolves a live account mailbox and localizes the bound plugin's name.
// Empty or reserved addresses skip transport successfully. During version replacement,
// the previous snapshot supplies naming for callbacks admitted by the older process.
//
// The notification service chooses recipient language before this adapter localizes
// the manifest name, and that language is passed back explicitly for rendering.
// The same source identity and reminder key are reused by notification markers.
// Transport acceptance and invocation flags remain available even when a later
// notification marker fails, allowing the plugin ledger to record a known outcome.
func (s *pluginEmailSender) Deliver(ctx context.Context, binding plugin.Binding, input plugin.EmailInput) (plugin.EmailDelivery, error) {
	account, err := s.users.GetByID(ctx, input.UserID)
	if err != nil {
		return plugin.EmailDelivery{}, pluginUserReadError(err)
	}
	address := strings.TrimSpace(account.Email)
	if address == "" || identity.IsReservedEmail(address) {
		return plugin.EmailDelivery{}, nil
	}
	installation, err := s.installations.Get(ctx, binding.PluginID)
	if err != nil {
		return plugin.EmailDelivery{}, err
	}
	snapshot := &installation.VersionSnapshot
	if snapshot.Version != binding.Version {
		if installation.Previous == nil || installation.Previous.Version != binding.Version {
			return plugin.EmailDelivery{}, plugin.ErrHostUnavailable
		}
		snapshot = installation.Previous
	}
	if snapshot.Manifest == nil {
		return plugin.EmailDelivery{}, plugin.ErrHostUnavailable
	}
	language := s.notifications.ResolveRecipientLocale(ctx, input.UserID, address)
	name := snapshot.Manifest.Name.Localize(language)
	if name == "" {
		name = binding.PluginID
	}
	recipientName := strings.TrimSpace(account.Username)
	if recipientName == "" {
		recipientName = notification.EmailRecipientName(address)
	}
	result, err := s.notifications.SendPluginMessage(ctx, notification.PluginMessage{
		PluginID: binding.PluginID, PluginName: name, Key: input.Key, UserID: input.UserID, Locale: language,
		Email: address, RecipientName: recipientName, Subject: input.Subject, BodyText: input.BodyText,
	})
	return plugin.EmailDelivery{Delivered: result.Delivered, Attempted: result.Attempted}, err
}
