package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// httpClient translates buffered HTTP contributions and safe transport failure classifications.
type httpClient struct {
	client pluginpb.HTTPHandlerClient // process-bound gRPC HTTP service client
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
			HTTP:   &httpClient{client: pluginpb.NewHTTPHandlerClient(client)},
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

// Exchange maps filtered contribution data onto the authenticated process transport.
// Caller grants remain inside the RPC request. The returned reply still requires
// panel validation of status, content type, response size, and allowed headers.
// Caller locale retains the host's negotiated language for SDK request handlers.
//
// Transport diagnostics are not browser-visible: deadline outcomes receive the
// dedicated timeout reason, and other RPC failures receive a generic gateway
// failure. Domain slices and maps remain stable for the duration of the call;
// protobuf response ownership transfers to this returned value after gRPC finishes
// decoding, without retaining a mutable caller request buffer in host state.
func (c *httpClient) Exchange(ctx context.Context, input *plugin.HTTPRequest) (*plugin.HTTPResponse, error) {
	request := &pluginpb.HTTPRequest{
		Method: input.Method, Path: input.Path, RawQuery: input.RawQuery,
		Headers: input.Headers, Body: input.Body, ClientIp: input.ClientIP,
	}
	if input.Caller != nil {
		request.Caller = &pluginpb.Caller{
			UserId:   input.Caller.UserID,
			Username: input.Caller.Username,
			Role:     input.Caller.Role,
			Locale:   input.Caller.Locale,
			Grant:    input.Caller.Grant,
		}
	}
	response, err := c.client.Handle(ctx, request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
			return nil, plugin.ErrPluginTimeout
		}
		return nil, plugin.ErrPluginRequestFailed
	}
	if response == nil {
		return nil, plugin.ErrPluginRequestFailed
	}
	return &plugin.HTTPResponse{Status: int(response.Status), Headers: response.Headers, Body: response.Body}, nil
}
