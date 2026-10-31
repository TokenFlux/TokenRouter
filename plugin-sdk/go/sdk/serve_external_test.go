package sdk_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/plugintest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// hostTransport dispenses test clients while retaining the authenticated broker.
type hostTransport struct {
	goplugin.NetRPCUnsupportedPlugin                      // disables legacy net/rpc test transport
	broker                           *goplugin.GRPCBroker // host callback broker connection
	connection                       *grpc.ClientConn     // plugin service client connection
}

// GRPCServer rejects server use of this client-only integration test adapter.
func (*hostTransport) GRPCServer(*goplugin.GRPCBroker, *grpc.Server) error {
	return fmt.Errorf("client-only test transport")
}

// GRPCClient retains the connected plugin endpoint and callback broker for assertions.
func (p *hostTransport) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, connection *grpc.ClientConn) (any, error) {
	p.broker, p.connection = broker, connection
	return p, nil
}

// TestServeConnectsRealProcessAndBroker builds and starts the example executable
// through go-plugin with automatic TLS and a verified binary digest. Lifecycle,
// HTTP, configuration, and host wallet callbacks must work over real connections.
func TestServeConnectsRealProcessAndBroker(t *testing.T) {
	if testing.Short() {
		t.Skip("real-process SDK smoke test")
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "plugin")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../examples/checkin/cmd/plugin")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, output)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	transport := &hostTransport{}
	command := exec.Command(binary)
	command.Env = []string{"GOMEMLIMIT=128MiB"}
	command.Dir = directory
	client := goplugin.NewClient(&goplugin.ClientConfig{HandshakeConfig: sdk.Handshake(), Plugins: goplugin.PluginSet{sdk.PluginKey: transport}, Cmd: command, AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC}, AutoMTLS: true, SkipHostEnv: true, Managed: false, SecureConfig: &goplugin.SecureConfig{Checksum: digest[:], Hash: sha256.New()}, Logger: hclog.NewNullLogger(), StartTimeout: 10 * time.Second})
	// Register process cleanup before establishing either connection, so failed handshake or initialization cannot leave the fixture executable running.
	t.Cleanup(client.Kill)
	protocol, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.Dispense(sdk.PluginKey); err != nil {
		t.Fatal(err)
	}
	host := plugintest.New(t)
	if err := host.Server.SetBalance(7, "1"); err != nil {
		t.Fatal(err)
	}
	brokerID := transport.broker.NextId()
	go transport.broker.AcceptAndServe(brokerID, func(options []grpc.ServerOption) *grpc.Server {
		server := grpc.NewServer(options...)
		pluginpb.RegisterHostServer(server, host.Server)
		return server
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lifecycle := pluginpb.NewPluginClient(transport.connection)
	_, err = lifecycle.Init(ctx, &pluginpb.InitRequest{HostVersion: "0.6.0", ApiVersion: 1, PluginId: "tokenflux.checkin", PluginVersion: "1.0.0", HostBrokerId: brokerID, ConfigJson: []byte(`{"reward":0.1}`), StateDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	health, err := lifecycle.Health(ctx, &pluginpb.HealthRequest{})
	if err != nil || !health.Ready {
		t.Fatalf("health: %v %v", health, err)
	}
	httpClient := pluginpb.NewHTTPHandlerClient(transport.connection)
	request := &pluginpb.HTTPRequest{Method: "POST", Path: "/checkin", Caller: &pluginpb.Caller{UserId: 7, Username: "tester", Role: "user"}, ClientIp: "192.0.2.1"}
	response, err := httpClient.Handle(ctx, request)
	if err != nil || response.Status != 200 {
		t.Fatalf("check-in: %v %v", response, err)
	}
	wallet, err := host.Env.Host.Wallet.Get(ctx, 7)
	if err != nil || wallet.Balance != "1.10000000" {
		t.Fatalf("wallet callback: %v %v", wallet, err)
	}
	response, err = httpClient.Handle(ctx, request)
	if err != nil || response.Status != 409 {
		t.Fatalf("repeat check-in: %v %v", response, err)
	}
	if _, err := lifecycle.ConfigChanged(ctx, &pluginpb.ConfigChangedRequest{ConfigJson: []byte(`{"reward":0.2}`)}); err != nil {
		t.Fatal(err)
	}
	response, err = httpClient.Handle(ctx, &pluginpb.HTTPRequest{Method: "GET", Path: "/status", Caller: request.Caller})
	if err != nil || response.Status != 200 {
		t.Fatalf("updated status: %v %v", response, err)
	}
	if _, err := lifecycle.Shutdown(ctx, &pluginpb.ShutdownRequest{}); err != nil {
		t.Fatal(err)
	}
}
