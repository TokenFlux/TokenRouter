package provider

import (
	"context"
	"fmt"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
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

// taskClient adapts the transport task client to the scheduler's contribution contract.
type taskClient struct {
	client pluginpb.TaskRunnerClient // process-bound gRPC task service client
}

// GRPCServer rejects server registration from this host-side connection adapter.
func (*grpcPlugin) GRPCServer(*goplugin.GRPCBroker, *grpc.Server) error {
	return fmt.Errorf("host plugin adapter cannot serve plugin contributions")
}

// GRPCClient creates typed clients without initiating any contribution calls.
func (*grpcPlugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, client *grpc.ClientConn) (any, error) {
	return &connection{
		services: &plugin.Conn{
			Plugin: pluginpb.NewPluginClient(client),
			HTTP:   pluginpb.NewHTTPHandlerClient(client),
			Tasks:  &taskClient{client: pluginpb.NewTaskRunnerClient(client)},
		},
		broker: broker,
	}, nil
}

// RunTask submits the scheduler's named occurrence without exposing transport call options.
// The process lease owner supplies cancellation and retains the connection until
// return. Scheduled time crosses the protocol boundary as whole Unix seconds.
func (c *taskClient) RunTask(ctx context.Context, name string, scheduled time.Time) error {
	_, err := c.client.Run(ctx, &pluginpb.RunTaskRequest{Name: name, ScheduledUnix: scheduled.Unix()})
	return err
}
