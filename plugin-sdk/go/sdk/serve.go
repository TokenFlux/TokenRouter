package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// serve.go starts the plugin's authenticated gRPC process endpoint and its
// parent watchdog. HashiCorp go-plugin owns handshake and transport listeners;
// the SDK registers lifecycle and implemented contribution services on the
// same server. A unary recovery interceptor turns handler panics into RPC
// errors while preserving process availability for subsequent host calls.
//
// The watchdog provides a cross-platform fallback when the operating system
// does not arrange parent-death cleanup. Its platform monitor checks the captured
// parent every two seconds and exits when that ownership ends. Normal
// server return cancels this goroutine. Process isolation remains a trust
// boundary for availability only; this entry point provides no filesystem,
// network, or system-user sandbox for administrator-installed plugin code.

const (
	// Handshake values identify the TokenRouter protocol independently of publisher identity; the host verifies each installed executable separately.
	ProtocolVersion  = 1
	MagicCookieKey   = "TOKENROUTER_PLUGIN"
	MagicCookieValue = "7d1f0c64-1f0a-4a55-9c2a-5b8f3c7e2a19"
	PluginKey        = "tokenrouter"
	MaxMessageBytes  = 16 << 20
)

// transport registers one plugin implementation on its go-plugin gRPC server.
type transport struct {
	goplugin.NetRPCUnsupportedPlugin              // disables legacy net/rpc transport
	implementation                   Plugin       // author-provided lifecycle implementation
	logger                           *slog.Logger // structured plugin diagnostic logger
}

// Handshake returns the fixed protocol identity shared by plugins and hosts.
func Handshake() goplugin.HandshakeConfig {
	return goplugin.HandshakeConfig{ProtocolVersion: ProtocolVersion, MagicCookieKey: MagicCookieKey, MagicCookieValue: MagicCookieValue}
}

// Serve runs a plugin from its executable's main function until host shutdown.
// Init receives an authenticated callback connection; no host credentials are
// inherited or reconstructed here. Authors should pass a single implementation
// and let this entry point own transport listeners and process lifecycle.
//
// Server options preserve go-plugin's automatic TLS configuration and add
// bounded messages plus panic recovery. A parent watchdog runs for the same
// lifetime as the server and exits orphaned processes even on platforms where
// the host cannot install a parent-death signal. Business work must still
// cooperate with context deadlines to finish within graceful shutdown budgets.
// @project-doc docs/interfaces/plugin_contracts.md#process_protocol
func Serve(implementation Plugin) {
	logger := hclog.New(&hclog.LoggerOptions{Name: "plugin", JSONFormat: true, Output: os.Stderr, Level: hclog.Info})
	slogLogger := slog.New(&logHandler{logger: logger})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Keep orphan detection independent of plugin callbacks so blocked application work cannot prevent the watchdog from noticing parent replacement.
	go watchParent(ctx, os.Getppid())
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake(),
		Plugins:         goplugin.PluginSet{PluginKey: &transport{implementation: implementation, logger: slogLogger}},
		Logger:          logger,
		GRPCServer: func(options []grpc.ServerOption) *grpc.Server {
			options = append(options, grpc.MaxRecvMsgSize(MaxMessageBytes), grpc.MaxSendMsgSize(MaxMessageBytes), grpc.UnaryInterceptor(recoverRPC(slogLogger)))
			return grpc.NewServer(options...)
		},
	})
}

// GRPCServer registers lifecycle and only the contributions implemented by the author.
func (t *transport) GRPCServer(broker *goplugin.GRPCBroker, server *grpc.Server) error {
	if t.implementation == nil {
		return fmt.Errorf("plugin implementation is required")
	}
	adapter := newServer(t.implementation, broker, t.logger)
	pluginpb.RegisterPluginServer(server, adapter)
	if _, ok := t.implementation.(HTTPPlugin); ok {
		pluginpb.RegisterHTTPHandlerServer(server, adapter)
	}
	if _, ok := t.implementation.(TaskPlugin); ok {
		pluginpb.RegisterTaskRunnerServer(server, adapter)
	}
	return nil
}

// GRPCClient rejects accidental use of the plugin-side adapter as a host client.
func (*transport) GRPCClient(context.Context, *goplugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, fmt.Errorf("plugin-side transport cannot create a host client")
}

// recoverRPC converts panics on the RPC goroutine into an internal status.
// It logs the method and panic value but leaves response details generic.
// Panics in author-created goroutines remain the author's responsibility.
func recoverRPC(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("Plugin callback panicked", "method", info.FullMethod, "panic", fmt.Sprint(recovered))
				err = status.Error(codes.Internal, "plugin callback panicked")
			}
		}()
		return handler(ctx, request)
	}
}
