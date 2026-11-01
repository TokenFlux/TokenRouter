package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// host_kv_test.go checks large KV pages through the authenticated callback
// adapter and its production message limits. Valid individual records must
// remain accessible over the SDK connection when their combined collection
// requires several byte-bounded response pages.

// hostKVListFixture supplies ordered immutable records for callback pagination tests.
type hostKVListFixture struct {
	plugin.KVStore // unused persistence capability methods
	// entries is initialized before RPC calls; returned pages share only immutable
	// JSON payloads with these ordered records until the test server stops.
	entries []plugin.KVEntry
}

// KVList returns a detached page slice after the supplied exclusive cursor.
// Payloads remain immutable and shared by the fixture. The core capability
// applies its public count and byte budgets to these storage results.
func (f *hostKVListFixture) KVList(_ context.Context, _ string, input plugin.KVListInput) ([]plugin.KVEntry, error) {
	result := make([]plugin.KVEntry, 0, input.Limit)
	for _, entry := range f.entries {
		if entry.Key <= input.Cursor {
			continue
		}
		result = append(result, entry)
		if len(result) == input.Limit {
			break
		}
	}
	return result, nil
}

// TestHostKVListTraversesLargeCollection requests a collection exceeding the
// transport ceiling through a real callback server and SDK client. Every response
// fits the unchanged gRPC limit, and following returned cursors retrieves all
// records without omissions or duplicates.
func TestHostKVListTraversesLargeCollection(t *testing.T) {
	store := &hostKVListFixture{}
	value := []byte(`"` + strings.Repeat("x", plugin.KVMaxValueBytes-2) + `"`)
	// Three hundred maximum-size values exceed the production transport ceiling.
	want := make([]string, 300)
	for index := range want {
		want[index] = fmt.Sprintf("%04d", index)
		store.entries = append(store.entries, plugin.KVEntry{Key: want[index], Value: value, Version: 1})
	}
	implementation := NewHostServices(HostCapabilities{KV: plugin.NewKV(store, nil)}, nil, nil).
		Bind(plugin.ProcessSpec{PluginID: "test.pages", Version: "1.0.0"})
	server := newHostGate(nil).server(nil, implementation, newHostCalls(slog.Default()))
	listener := bufconn.Listen(1 << 20)
	// The owned in-memory server retains production callback limits; cleanup
	// closes its listener and stops transport after assertions finish.
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient("passthrough:///plugin-kv-pages",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(sdk.MaxMessageBytes), grpc.MaxCallSendMsgSize(sdk.MaxMessageBytes)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	host := sdk.NewHost(pluginpb.NewHostClient(connection))
	request := &pluginpb.KVListRequest{Collection: "data", Limit: 500}
	var observed []string
	for range want {
		response, err := host.KV.List(t.Context(), request)
		require.NoError(t, err)
		require.NotEmpty(t, response.Items)
		require.Less(t, proto.Size(response), sdk.MaxMessageBytes)
		for _, entry := range response.Items {
			require.Equal(t, value, entry.ValueJson)
			observed = append(observed, entry.Key)
		}
		if response.NextCursor == "" {
			break
		}
		require.Equal(t, response.Items[len(response.Items)-1].Key, response.NextCursor)
		request.Cursor = response.NextCursor
	}
	require.Equal(t, want, observed)
}
