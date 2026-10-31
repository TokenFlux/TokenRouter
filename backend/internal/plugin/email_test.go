package plugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmailRejectsUntrustedArgumentsBeforeClaim checks permission, identity, content, and key bounds without configured storage or transport.
// Every rejection completes before dependencies are used, preventing invalid requests from consuming a durable claim or sending a message.
func TestEmailRejectsUntrustedArgumentsBeforeClaim(t *testing.T) {
	service := NewEmails(nil, nil, nil, nil, nil)
	valid := EmailInput{UserID: 7, Subject: "Reward", BodyText: "Hello", Key: "key"}
	_, err := service.Send(t.Context(), Binding{}, valid)
	require.ErrorIs(t, err, ErrPermissionNotGranted)
	binding := Binding{PluginID: "test.mail", Permissions: []string{"notify.email"}}
	for _, input := range []EmailInput{
		{Subject: "Reward", BodyText: "Hello", Key: "key"},
		{UserID: 7, Subject: " ", BodyText: "Hello", Key: "key"},
		{UserID: 7, Subject: strings.Repeat("界", 121), BodyText: "Hello", Key: "key"},
		{UserID: 7, Subject: "Hello\nBcc: victim", BodyText: "Hello", Key: "key"},
		{UserID: 7, Subject: "Reward", BodyText: "", Key: "key"},
		{UserID: 7, Subject: "Reward", BodyText: strings.Repeat("界", 8001), Key: "key"},
		{UserID: 7, Subject: "Reward", BodyText: "nul\x00", Key: "key"},
		{UserID: 7, Subject: "Reward", BodyText: "bad\xff", Key: "key"},
		{UserID: 7, Subject: "Reward", BodyText: "Hello", Key: "bad key"},
	} {
		_, err := service.Send(t.Context(), binding, input)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
}
