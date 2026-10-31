package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// host_kv.go maps protocol storage requests onto the bound plugin namespace.
// Request messages carry collection and key names but never select a plugin
// identity. The core capability validates inputs and quota admission, while the
// PostgreSQL adapter owns version conditions, expiration, and atomic arithmetic.

// KVGet returns a live record or an explicit absent protocol result.
// Expired values never expose their stored bytes or version. The core adapter
// returns owned data that this response can pass directly to gRPC serialization.
func (s *hostService) KVGet(ctx context.Context, request *pluginpb.KVGetRequest) (*pluginpb.KVGetResponse, error) {
	entry, err := s.services.capabilities.KV.Get(ctx, s.binding, request.GetCollection(), request.GetKey())
	if err != nil {
		return nil, capabilityError(err)
	}
	if entry == nil {
		return &pluginpb.KVGetResponse{}, nil
	}
	return &pluginpb.KVGetResponse{Found: true, ValueJson: entry.Value, Version: entry.Version}, nil
}

// KVPut translates one JSON replacement with its condition and expiration.
func (s *hostService) KVPut(ctx context.Context, request *pluginpb.KVPutRequest) (*pluginpb.KVPutResponse, error) {
	version, err := s.services.capabilities.KV.Put(ctx, s.binding, plugin.KVPutInput{
		Collection: request.GetCollection(),
		Key:        request.GetKey(),
		Value:      request.GetValueJson(),
		IfVersion:  request.GetIfVersion(),
		TTLSeconds: request.GetTtlSeconds(),
	})
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.KVPutResponse{Version: version}, nil
}

// KVDelete translates a conditional deletion and preserves its logical existence result.
func (s *hostService) KVDelete(ctx context.Context, request *pluginpb.KVDeleteRequest) (*pluginpb.KVDeleteResponse, error) {
	deleted, err := s.services.capabilities.KV.Delete(ctx, s.binding, request.GetCollection(), request.GetKey(), request.GetIfVersion())
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.KVDeleteResponse{Deleted: deleted}, nil
}

// KVList maps one ordered page without changing its exclusive continuation key.
func (s *hostService) KVList(ctx context.Context, request *pluginpb.KVListRequest) (*pluginpb.KVListResponse, error) {
	page, err := s.services.capabilities.KV.List(ctx, s.binding, plugin.KVListInput{
		Collection: request.GetCollection(),
		Prefix:     request.GetPrefix(),
		Cursor:     request.GetCursor(),
		Limit:      int(request.GetLimit()),
	})
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.KVListResponse{NextCursor: page.NextCursor, Items: make([]*pluginpb.KVItem, 0, len(page.Items))}
	for _, entry := range page.Items {
		response.Items = append(response.Items, &pluginpb.KVItem{Key: entry.Key, ValueJson: entry.Value, Version: entry.Version})
	}
	return response, nil
}

// KVIncr forwards a signed increment under the authenticated plugin's storage identity.
func (s *hostService) KVIncr(ctx context.Context, request *pluginpb.KVIncrRequest) (*pluginpb.KVIncrResponse, error) {
	result, err := s.services.capabilities.KV.Incr(ctx, s.binding, request.GetCollection(), request.GetKey(), request.GetDelta(), request.GetTtlSeconds())
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.KVIncrResponse{Value: result.Value, Version: result.Version}, nil
}
