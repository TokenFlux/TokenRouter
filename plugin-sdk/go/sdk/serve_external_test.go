package sdk_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

// serve_external_test.go verifies the public SDK against an isolated child
// process and a real authenticated host broker. Its internal fixture exercises
// transport and lifecycle behavior without importing a separately maintained
// plugin or requiring a downloaded example repository.

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
	p.broker = broker
	p.connection = connection
	return p, nil
}

// TestServeConnectsRealProcessAndBroker builds and starts a minimal fixture executable
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
	build := exec.Command("go", "build", "-o", binary, "../internal/testplugins/roundtrip")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
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
	_, err = lifecycle.Init(ctx, &pluginpb.InitRequest{
		HostVersion:   "0.6.0",
		ApiVersion:    1,
		PluginId:      "test.roundtrip",
		PluginVersion: "1.0.0",
		HostBrokerId:  brokerID,
		ConfigJson:    []byte(`{"amount":"0.1"}`),
		StateDir:      directory,
	})
	if err != nil {
		t.Fatal(err)
	}
	health, err := lifecycle.Health(ctx, &pluginpb.HealthRequest{})
	if err != nil || !health.Ready {
		t.Fatalf("health: %v %v", health, err)
	}
	httpClient := pluginpb.NewHTTPHandlerClient(transport.connection)
	request := &pluginpb.HTTPRequest{
		Method:   "POST",
		Path:     "/echo",
		RawQuery: "value=hello%20world",
		Headers:  map[string]string{"X-Test-Key": "first"},
		Body:     []byte("request payload"),
		Caller:   &pluginpb.Caller{UserId: 7, Username: "tester", Role: "user"},
		ClientIp: "192.0.2.1",
	}
	response, err := httpClient.Handle(ctx, request)
	if err != nil || response.Status != 200 {
		t.Fatalf("roundtrip: %v %v", response, err)
	}
	var echo map[string]string
	if err := json.Unmarshal(response.Body, &echo); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"method": "POST", "path": "/echo", "query": "value=hello%20world",
		"body": "request payload", "user": "7", "username": "tester",
		"plugin": "test.roundtrip", "amount": "0.1", "balance": "1.10000000",
	} {
		if echo[key] != want {
			t.Fatalf("echo %s = %q, want %q", key, echo[key], want)
		}
	}
	if response.Headers["X-Test-Reply"] != "roundtrip" {
		t.Fatalf("response headers: %v", response.Headers)
	}
	wallet, err := host.Env.Host.Wallet.Get(ctx, 7)
	if err != nil || wallet.Balance != "1.10000000" {
		t.Fatalf("wallet callback: %v %v", wallet, err)
	}
	if _, err := lifecycle.ConfigChanged(ctx, &pluginpb.ConfigChangedRequest{ConfigJson: []byte(`{"amount":"0.2"}`)}); err != nil {
		t.Fatal(err)
	}
	request.Headers["X-Test-Key"] = "second"
	response, err = httpClient.Handle(ctx, request)
	if err != nil || response.Status != 200 {
		t.Fatalf("updated roundtrip: %v %v", response, err)
	}
	if err := json.Unmarshal(response.Body, &echo); err != nil {
		t.Fatal(err)
	}
	if echo["amount"] != "0.2" || echo["balance"] != "1.30000000" {
		t.Fatalf("updated configuration: %v", echo)
	}
	if _, err := lifecycle.Shutdown(ctx, &pluginpb.ShutdownRequest{}); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(directory, "shutdown"))
	if err != nil || string(marker) != "complete" {
		t.Fatalf("shutdown callback: %q %v", marker, err)
	}
	health, err = lifecycle.Health(ctx, &pluginpb.HealthRequest{})
	if err != nil || health.Ready {
		t.Fatalf("health after shutdown: %v %v", health, err)
	}
}
