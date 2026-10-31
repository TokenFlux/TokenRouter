package provider

import (
	"context"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// grpc_plugin.go connects go-plugin's single protocol registration to the
// service clients retained by runtime leases. The broker remains with the
// process owner for authenticated host callbacks, while consumers receive only
// typed contribution clients and never manage transport listeners or certificates.

// grpcPlugin dispenses service clients and the broker for one process connection.
type grpcPlugin struct {
	goplugin.NetRPCUnsupportedPlugin // disables legacy net/rpc transport
}

// connection retains broker ownership beside the core's typed service clients.
type connection struct {
	services *plugin.Conn         // lease-visible plugin service clients
	broker   *goplugin.GRPCBroker // authenticated host callback broker
}

// GRPCServer rejects server registration from this host-side connection adapter.
func (*grpcPlugin) GRPCServer(*goplugin.GRPCBroker, *grpc.Server) error {
	return fmt.Errorf("host plugin adapter cannot serve plugin contributions")
}

// GRPCClient creates typed clients without initiating any contribution calls.
func (*grpcPlugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, client *grpc.ClientConn) (any, error) {
	return &connection{services: &plugin.Conn{Plugin: pluginpb.NewPluginClient(client), HTTP: pluginpb.NewHTTPHandlerClient(client), Tasks: pluginpb.NewTaskRunnerClient(client)}, broker: broker}, nil
}
