package provider

import (
	"context"
	"slices"
)

// model_sync.go fetches live model catalogues using copied provider records
// and tracked request lifetimes.
// Administrator model previews fetch a live catalogue using a provider snapshot
// whose credentials can be mutated by the adapter. The service copies that
// input before fetching and copies the returned model slice before handing it
// back to its caller, together with protocol suggestions from an optional
// platform hook. A nil suggestion map omits the JSON field; an empty map sends
// an empty object so forms can clear previously accepted catalog suggestions.
//
// The shared operationActivity implementation tracks these request-triggered
// queries, combines request and shutdown cancellation, and fixes the first stop
// result. Construction performs no upstream query; requests begin work when
// Query is called, and stopped services reject another query.

// ModelSyncService tracks request-triggered model queries through an injected
// fetch function.
type ModelSyncService struct {
	Qoder *QoderCatalogService // account model synchronization owner
	// fetch receives the service-owned provider copy for one registered query.
	// It observes cancellation so shutdown can wait for the call to return.
	fetch func(context.Context, *Record) ([]string, error)
	// activity owns admission, cancellation, and the retained shutdown result.
	// Each successful registration remains active until Query's deferred finish.
	activity operationActivity
	// Suggest maps queried model IDs to catalogue protocol suggestions for the
	// service's record copy. Its newly allocated map becomes the result's
	// ModelProtocols; a nil hook or nil map leaves model_protocols absent, while
	// an empty map replaces the form's accepted suggestions with an empty table.
	Suggest func(*Record, []string) map[string]string
}

// ModelSyncResult contains live identifiers and independently owned catalogue suggestions.
type ModelSyncResult struct {
	SyncStatus map[string]any `json:"sync_status,omitempty"` // account catalog synchronization state
	Models     []string       `json:"models"`                // live upstream model identifiers
	// ModelProtocols contains catalogue suggestions for the returned models. Saved
	// administrator overrides remain owned by the provider configuration and form.
	// Nil omits the field; an empty map serializes as an object that clears the
	// form's accepted suggestions for the selected provider mode.
	ModelProtocols map[string]string `json:"model_protocols,omitzero"`
}

// NewModelSyncService constructs a request-triggered catalogue service around
// fetch. A nil function returns nil to represent an unavailable service, and
// construction performs no upstream request or persistence.
func NewModelSyncService(fetch func(context.Context, *Record) ([]string, error)) *ModelSyncService {
	if fetch == nil {
		return nil
	}
	return &ModelSyncService{fetch: fetch}
}

// Query registers a catalogue query and passes a copied provider record to the
// adapter. Its result pairs live model IDs with the Suggest hook's catalogue
// suggestions for the same copy, leaving saved manual protocol overrides in the
// caller-owned configuration.
//
// Request cancellation and service shutdown reach the adapter through the
// registered context. Completion unregisters the operation so StopContext can
// finish waiting, and a stopped service returns ErrRefreshStopped before
// invoking fetch. Callers provide stable input during copying and adapters
// respond to cancellation to permit timely shutdown.
func (s *ModelSyncService) Query(ctx context.Context, v *Record) (ModelSyncResult, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrRefreshStopped)
	if err != nil {
		return ModelSyncResult{}, err
	}
	defer finish()
	snapshot := CloneRecord(v)
	if snapshot != nil && snapshot.IsQoderCosy() && s.Qoder != nil {
		if snapshot.ID <= 0 {
			return ModelSyncResult{}, &UpstreamModelSyncError{Kind: UpstreamModelSyncErrorUnsupported, Message: "Save the Qoder account before synchronizing its models"}
		}
		observed, err := s.Qoder.Refresh(ctx, snapshot.ID)
		if err != nil {
			return ModelSyncResult{}, &UpstreamModelSyncError{Kind: UpstreamModelSyncErrorUpstream, Message: "Failed to synchronize Qoder account models", Err: err}
		}
		// Refresh rereads saved credentials and conditionally publishes their result.
		// Return that accepted observation rather than validating it against this
		// handler's potentially older credential copy and reporting a false empty list.
		return ModelSyncResult{Models: qoderCatalogModelIDs(observed), SyncStatus: observed.adminStatus()}, nil
	}
	models, err := s.fetch(ctx, snapshot)
	if err != nil {
		return ModelSyncResult{}, err
	}
	result := ModelSyncResult{Models: slices.Clone(models)}
	if s.Suggest != nil {
		result.ModelProtocols = s.Suggest(snapshot, models)
	}
	return result, nil
}

// StopContext rejects subsequent model queries, cancels active queries, and
// waits within ctx's budget. The shared activity tracker retains the first stop
// outcome, including a timeout while an adapter remains unfinished.
func (s *ModelSyncService) StopContext(ctx context.Context) error {
	return s.activity.stop(ctx, "provider model list requests")
}
