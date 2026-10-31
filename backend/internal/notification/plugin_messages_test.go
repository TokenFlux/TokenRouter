package notification

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mailtest "github.com/TokenFlux/TokenRouter/internal/notification/testkit"
)

// pluginMessageDelivery records sequential notification attempts and injected SMTP failures.
type pluginMessageDelivery struct {
	subject string // captured message subject text
	body    string // captured rendered HTML body
	calls   int    // observed transport invocation count
	err     error  // injected transport return error
}

// pluginMarkerFailure preserves settings reads while rejecting only delivery success markers.
type pluginMarkerFailure struct {
	*mailtest.MemorySettings // shared in-memory notification settings
}

// SendEmail captures rendered content before returning the configured transport outcome.
func (s *pluginMessageDelivery) SendEmail(_ context.Context, _, subject, body string) error {
	s.calls++
	s.subject = subject
	s.body = body
	return s.err
}

// Set rejects success markers after transport but permits preference and secret persistence.
func (s *pluginMarkerFailure) Set(ctx context.Context, key, value string) error {
	if strings.HasPrefix(key, notificationEmailDeliveryKeyPrefix) {
		return errors.New("injected marker failure")
	}
	return s.MemorySettings.Set(ctx, key, value)
}

// TestPluginMessageEscapesContentAndPreservesDeliveryResult sends both template languages with hostile HTML and mixed line endings.
// Captured content stays escaped, identifies its plugin, and reuses a success marker without another transport call.
func TestPluginMessageEscapesContentAndPreservesDeliveryResult(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			sender := &pluginMessageDelivery{}
			service := NewNotificationEmailService(mailtest.NewMemorySettings(), sender)
			input := PluginMessage{
				PluginID: "test.mail", PluginName: "Check <in>", Key: "notice-one", UserID: 7,
				Locale: language, Email: "reader@example.com", RecipientName: "Reader",
				Subject: "Reward", BodyText: "<script>alert('x')</script>\r\nLine & two\rEnd",
			}
			result, err := service.SendPluginMessage(t.Context(), input)
			require.NoError(t, err)
			require.Equal(t, SendResult{Delivered: true, Attempted: true}, result)
			require.Equal(t, "Check <in>: Reward", sender.subject)
			require.Contains(t, sender.body, "&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt;<br>Line &amp; two<br>End")
			require.NotContains(t, sender.body, "<script>")
			require.Contains(t, sender.body, "Check &lt;in&gt;")
			require.Contains(t, sender.body, "email-unsubscribe?token=")
			if language == "en" {
				require.Contains(t, sender.body, "This email was sent by the plugin")
			} else {
				require.Contains(t, sender.body, "此邮件由插件")
			}
			result, err = service.SendPluginMessage(t.Context(), input)
			require.NoError(t, err)
			require.Equal(t, SendResult{Delivered: true}, result)
			require.Equal(t, 1, sender.calls)
		})
	}
}

// TestPluginMessageSkippedAndFailedOutcomes distinguishes unsubscribed or missing recipients from SMTP failure and marker failure.
// Only transport invocation sets Attempted, and successful acceptance remains visible when the subsequent marker cannot be saved.
func TestPluginMessageSkippedAndFailedOutcomes(t *testing.T) {
	sender := &pluginMessageDelivery{}
	service := NewNotificationEmailService(mailtest.NewMemorySettings(), sender)
	input := PluginMessage{PluginID: "test.mail", PluginName: "Check in", Key: "notice", Email: "reader@example.com", Subject: "Subject", BodyText: "Body"}
	token, err := service.createUnsubscribeToken(t.Context(), input.Email, NotificationEmailEventPluginMessage)
	require.NoError(t, err)
	_, err = service.Unsubscribe(t.Context(), token)
	require.NoError(t, err)
	result, err := service.SendPluginMessage(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, SendResult{}, result)
	require.Zero(t, sender.calls)
	input.Email = ""
	result, err = service.SendPluginMessage(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, SendResult{}, result)
	require.Zero(t, sender.calls)
	input.Email = "other@example.com"
	sender.err = errors.New("injected SMTP failure")
	result, err = service.SendPluginMessage(t.Context(), input)
	require.Error(t, err)
	require.Equal(t, SendResult{Attempted: true}, result)
	sender.err = nil
	service = NewNotificationEmailService(&pluginMarkerFailure{MemorySettings: mailtest.NewMemorySettings()}, sender)
	result, err = service.SendPluginMessage(t.Context(), input)
	require.ErrorContains(t, err, "injected marker failure")
	require.Equal(t, SendResult{Delivered: true, Attempted: true}, result)
}
