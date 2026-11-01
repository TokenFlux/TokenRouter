package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"strings"
)

// plugin_messages.go adapts validated plain-text plugin notices to the shared
// notification renderer. Plugin text is escaped before line breaks become trusted
// HTML, while names and subjects remain ordinary escaped template variables.
// Delivery retains the existing recipient language, optional-event unsubscribe,
// source marker, and transport behavior through SendWithResult. Marker identity
// hashes the original operation key before notification normalization, preserving
// distinctions that the plugin ledger also retains.
//
// The marker namespace excludes the old readable reminder format, whose case
// folding and punctuation replacement could merge separate operations. Existing
// plugin ledger claims remain authoritative: completed calls
// replay their result, and uncertain claims never reach this sender again. New
// claims therefore require only the distinct marker for their exact operation.

const (
	// NotificationEmailEventPluginMessage identifies optional plugin notices consistently across templates, unsubscribe preferences, and source-backed delivery markers.
	NotificationEmailEventPluginMessage = "plugin_message"
)

// pluginMessageTemplates keeps the built-in language variants together while the central event registry owns availability and placeholder validation.
var pluginMessageTemplates = map[string]notificationEmailOfficialTemplate{
	notificationEmailDefaultLocale: {
		Subject: "{{plugin_name}}: {{subject}}",
		HTML:    notificationEmailCard("#12A7E8", "{{plugin_name}}", `<p>Hello {{recipient_name}},</p><div>{{body_html}}</div><p>This email was sent by the plugin {{plugin_name}}.</p><p><a href="{{unsubscribe_url}}">Unsubscribe from plugin messages</a></p>`),
	},
	notificationEmailLocaleChinese: {
		Subject: "{{plugin_name}}: {{subject}}",
		HTML:    notificationEmailCard("#12A7E8", "{{plugin_name}}", `<p>{{recipient_name}}，你好：</p><div>{{body_html}}</div><p>此邮件由插件 {{plugin_name}} 发送。</p><p><a href="{{unsubscribe_url}}">退订插件消息</a></p>`),
	},
}

// PluginMessage supplies resolved recipient details and validated plain-text plugin content.
type PluginMessage struct {
	PluginID      string // authenticated plugin installation identity
	PluginName    string // recipient-localized plugin display name
	Key           string // plugin-wide operation replay key
	UserID        int64  // target account language identifier
	Locale        string // resolved recipient language tag
	Email         string // resolved recipient mailbox address
	RecipientName string // resolved recipient display name
	Subject       string // validated plain-text message subject
	BodyText      string // validated plain-text message body
}

// SendPluginMessage renders plain text through the optional plugin-message event.
// Its caller validates subject/body limits and supplies a mailbox resolved by
// existing account rules. HTML escaping precedes line-break conversion, so plugin
// text cannot introduce tags or attributes into the trusted body substitution.
//
// Operation keys are case-sensitive and may contain punctuation that notification
// marker normalization replaces. Hashing their original bytes produces a safe
// marker component without losing ledger identity. The new reminder namespace
// deliberately excludes older colliding markers; the plugin ledger independently
// prevents another transport attempt for an operation that already has a claim.
func (s *NotificationEmailService) SendPluginMessage(ctx context.Context, input PluginMessage) (SendResult, error) {
	text := strings.ReplaceAll(input.BodyText, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	body := strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	// Preserve original key bytes before notification normalization; the separate
	// namespace prevents an older ambiguous marker from suppressing this new claim.
	digest := sha256.Sum256([]byte(input.Key))
	reminder := "plugin-message-v1." + hex.EncodeToString(digest[:])
	return s.SendWithResult(ctx, SendRequest{
		Event: NotificationEmailEventPluginMessage, Locale: input.Locale, RecipientEmail: input.Email, RecipientName: input.RecipientName, UserID: input.UserID,
		SourceType: "plugin", SourceID: input.PluginID, ReminderKey: reminder,
		Variables:        map[string]string{"plugin_name": input.PluginName, "subject": input.Subject, "body_html": text},
		RawHTMLVariables: map[string]string{"body_html": body},
	})
}
