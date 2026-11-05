package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wshttp "github.com/TokenFlux/TokenRouter/internal/gateway/ws/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
)

// gateway_ws_connections.go constructs Responses WS connection resources and
// binds runtime option refresh. Responses WS option refresh updates the same
// lazily created connection pool used by execution. The refresh hook stops at
// level nine before connections close at level ten, and Codex turn origins
// receive the selection service's current sticky lifetime.

// provideCodexTurnStateHeaders binds a new origin registry and selection TTL
// reads.
func provideCodexTurnStateHeaders(choices *selection.Compatible) *gatewayhttp.CodexTurnStateHeaders {
	return &gatewayhttp.CodexTurnStateHeaders{Origins: session.NewCodexTurnOrigins(time.Now), TTL: choices.SessionStickyTTL}
}

// provideWSConnections creates the on-demand connection owner and binds runtime
// option application. Call before lifecycle startup with runtime and manager;
// the refresh callback updates this same owner, whose registered close precedes
// later request and shared-storage cleanup.
func provideWSConnections(cfg *config.Config, manager *lifecycle.Manager, runtime *ws.Runtime) *openaiws.OpenAIWSConnections {
	connections := openaiws.NewOpenAIWSConnections(openAIWSPoolOptions(cfg), openai.NewDefaultWSClientDialer())
	runtime.SetApply(func(value ws.Parameters) error { return connections.UpdateOptions(responsesWSPoolOptions(value)) })
	// The settings hook stops one level earlier. Close this connection owner
	// after refresh callbacks finish so online updates cannot race a closed pool.
	manager.Register(lifecycle.Hook{Name: "OpenAIWSConnections", StartOrder: 990, StopOrder: 10, Stop: func(context.Context) error { connections.Close(); return nil }})
	return connections
}

// openAIWSPoolOptions returns nil when cfg is absent or extracts its pool
// settings. Configured zero values pass through execution options and are left
// for the connection owner's normalization.
func openAIWSPoolOptions(cfg *config.Config) *openaiws.WSPoolOptions {
	if cfg == nil {
		return nil
	}
	returnValue := openAIWSExecutionOptions(cfg)
	returnPtr := responsesWSPoolOptions(*returnValue)
	return &returnPtr
}

// responsesWSPoolOptions selects connection capacity, prewarm, queue, and dial
// settings.
func responsesWSPoolOptions(options ws.Parameters) openaiws.WSPoolOptions {
	return openaiws.WSPoolOptions{
		MaxConnsPerProvider:                         options.MaxConnsPerProvider,
		DynamicMaxConnsByProviderConcurrencyEnabled: options.DynamicMaxConnsByProviderConcurrencyEnabled,
		OAuthMaxConnsFactor:                         options.OAuthMaxConnsFactor,
		APIKeyMaxConnsFactor:                        options.APIKeyMaxConnsFactor,
		MinIdlePerProvider:                          options.MinIdlePerProvider,
		MaxIdlePerProvider:                          options.MaxIdlePerProvider,
		QueueLimitPerConn:                           options.QueueLimitPerConn,
		PoolTargetUtilization:                       options.PoolTargetUtilization,
		PrewarmCooldownMS:                           options.PrewarmCooldownMS,
		DialTimeoutSeconds:                          options.DialTimeoutSeconds,
	}
}

// openAIWSExecutionOptions returns nil for missing cfg or copies all WS
// options. Zero values are retained so downstream resolvers can apply their own
// defaults and disabled-feature interpretations.
func openAIWSExecutionOptions(cfg *config.Config) *wshttp.OpenAIWSOptions {
	if cfg == nil {
		return nil
	}
	v := cfg.Gateway.OpenAI.ResponsesWS
	return &wshttp.OpenAIWSOptions{
		MaxConnsPerProvider:                         v.MaxConnsPerProvider,
		MinIdlePerProvider:                          v.MinIdlePerProvider,
		MaxIdlePerProvider:                          v.MaxIdlePerProvider,
		DynamicMaxConnsByProviderConcurrencyEnabled: v.DynamicMaxConnsByProviderConcurrencyEnabled,
		OAuthMaxConnsFactor:                         v.OAuthMaxConnsFactor,
		APIKeyMaxConnsFactor:                        v.APIKeyMaxConnsFactor,
		QueueLimitPerConn:                           v.QueueLimitPerConn,
		PoolTargetUtilization:                       v.PoolTargetUtilization,
		PrewarmCooldownMS:                           v.PrewarmCooldownMS,
		ClientFirstMessageTimeoutSeconds:            v.ClientFirstMessageTimeoutSeconds,
		IngressInterTurnIdleTimeoutSeconds:          v.IngressInterTurnIdleTimeoutSeconds,
		MaxIngressConnectionsPerAPIKey:              v.MaxIngressConnectionsPerAPIKey,
		ClientReadLimitBytes:                        v.ClientReadLimitBytes,
		HTTPBridgeThresholdBytes:                    v.HTTPBridgeThresholdBytes,
		DialTimeoutSeconds:                          v.DialTimeoutSeconds,
		ReadTimeoutSeconds:                          v.ReadTimeoutSeconds,
		WriteTimeoutSeconds:                         v.WriteTimeoutSeconds,
		IngressPreviousResponseRecoveryEnabled:      v.IngressPreviousResponseRecoveryEnabled,
		StickySessionTTLSeconds:                     cfg.Scheduling.SessionAffinity.TTLSeconds,
		StickyResponseIDTTLSeconds:                  cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds,
	}
}

// provideResponsesWSRuntime constructs online parameter refresh and registers
// its lifecycle. Defaults start from ws.DefaultParameters and configured fields
// replace them, including zero values; refresh stops before the connection
// owner closes so its callback cannot update that owner afterward.
func provideResponsesWSRuntime(cfg *config.Config, store *settings.Store, manager *lifecycle.Manager) *ws.Runtime {
	defaults := ws.DefaultParameters()
	if cfg != nil {
		v := cfg.Gateway.OpenAI.ResponsesWS
		defaults.MaxConnsPerProvider = v.MaxConnsPerProvider
		defaults.MinIdlePerProvider = v.MinIdlePerProvider
		defaults.MaxIdlePerProvider = v.MaxIdlePerProvider
		defaults.DynamicMaxConnsByProviderConcurrencyEnabled = v.DynamicMaxConnsByProviderConcurrencyEnabled
		defaults.OAuthMaxConnsFactor = v.OAuthMaxConnsFactor
		defaults.APIKeyMaxConnsFactor = v.APIKeyMaxConnsFactor
		defaults.QueueLimitPerConn = v.QueueLimitPerConn
		defaults.PoolTargetUtilization = v.PoolTargetUtilization
		defaults.PrewarmCooldownMS = v.PrewarmCooldownMS
		defaults.ClientFirstMessageTimeoutSeconds = v.ClientFirstMessageTimeoutSeconds
		defaults.IngressInterTurnIdleTimeoutSeconds = v.IngressInterTurnIdleTimeoutSeconds
		defaults.MaxIngressConnectionsPerAPIKey = v.MaxIngressConnectionsPerAPIKey
		defaults.ClientReadLimitBytes = v.ClientReadLimitBytes
		defaults.HTTPBridgeThresholdBytes = v.HTTPBridgeThresholdBytes
		defaults.DialTimeoutSeconds = v.DialTimeoutSeconds
		defaults.ReadTimeoutSeconds = v.ReadTimeoutSeconds
		defaults.WriteTimeoutSeconds = v.WriteTimeoutSeconds
		defaults.IngressPreviousResponseRecoveryEnabled = v.IngressPreviousResponseRecoveryEnabled
		defaults.StickySessionTTLSeconds = cfg.Scheduling.SessionAffinity.TTLSeconds
		defaults.StickyResponseIDTTLSeconds = cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds
	}
	runtime := ws.NewRuntime(defaults, store, func(err error) { slog.Warn("Failed to refresh Responses WS settings", "error", err) })
	// Seal settings refresh before the next connection-close level, retaining
	// the shared connection owner until every claimed option application has completed.
	manager.Register(lifecycle.Hook{Name: "ResponsesWSSettings", StartOrder: 980, StopOrder: 9, Start: runtime.Start, Stop: runtime.Stop})
	return runtime
}
