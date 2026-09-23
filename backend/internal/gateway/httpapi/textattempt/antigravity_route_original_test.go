package textattempt

import (
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestShouldUseAntigravityCompat(t *testing.T) {
	tests := []struct {
		name    string
		account *gatewayprovider.ExecutionAccount
		want    bool
	}{
		{"oauth", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeOAuth}}, true},
		{"setup token", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeSetupToken}}, false},
		{"upstream", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeUpstream}}, false},
		{"api key", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeAPIKey}}, false},
		{"anthropic oauth", &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldUseAntigravityCompat(tt.account))
		})
	}
}
