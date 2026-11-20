package provider

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

// antigravity_authorization.go binds proxy-specific clients, Cloud Resource
// Manager project fallback, and platform PKCE operations to the provider
// authorization workflow. The provider core receives these operations together
// with application logging and the supplied proxy resolver so its session
// workflow can perform each exchange when needed.

// AntigravityAuthorizationOptions binds platform OAuth operations and Resource
// Manager fallback for the provider core. It retains resolveProxy as supplied
// and constructs clients when the workflow requests them.
func AntigravityAuthorizationOptions(resolveProxy func(context.Context, int64) (string, bool)) provider.AntigravityAuthorizationOptions {
	return provider.AntigravityAuthorizationOptions{
		NewClient: func(proxy string) (provider.AntigravityAuthorizationClient, error) {
			return antigravity.NewClient(proxy)
		},
		ResolveProxy:          resolveProxy,
		FetchProject:          codeassist.FetchProjectIDFromResourceManager,
		GenerateState:         antigravity.GenerateState,
		GenerateCodeVerifier:  antigravity.GenerateCodeVerifier,
		GenerateSessionID:     antigravity.GenerateSessionID,
		GenerateCodeChallenge: antigravity.GenerateCodeChallenge,
		BuildAuthorizationURL: antigravity.BuildAuthorizationURL,
		IsConnectionError:     antigravity.IsConnectionError,
		Printf:                func(format string, args ...any) { _, _ = fmt.Printf(format, args...) },
		Warn:                  slog.Warn,
		Info:                  slog.Info,
	}
}
