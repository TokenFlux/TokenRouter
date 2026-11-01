package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// catalog_refresh.go coordinates shared refresh attempts and accepted-cache publication.
// Each source has one active transfer and one mutation gate. Waiter cancellation
// stops waiting without canceling work needed by another caller, while service
// shutdown cancels the worker context and waits for actual completion through
// CatalogService's lifecycle ownership.
//
// Verification and schema validation both finish before cache replacement. A
// rejected response updates only bounded diagnostics and any observed signature
// classification, retaining the previous document and accepted timestamp. Source
// configuration and deletion use the same gate, preventing an older fetch from
// publishing after a different URL, key, or source identity has taken ownership.

// Refresh waits for this source's shared attempt while preserving independent waiter cancellation.
// Service-owned work can continue after one HTTP caller leaves. Globally disabled
// or stopped services reject admission without starting transport or cache writes.
func (c *CatalogService) Refresh(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	flight, err := c.beginRefresh(id)
	if err != nil {
		return err
	}
	select {
	case <-flight.done:
		return flight.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RefreshAll attempts every enabled source and returns their collected failures.
// Successful sources still publish when another fails. Each attempt shares any
// work already in progress, and canceling this waiter does not discard accepted results.
func (c *CatalogService) RefreshAll(ctx context.Context) error {
	if !c.options.Enabled {
		return ErrPluginsDisabled
	}
	sources, err := c.indexes.ListIndexes(ctx)
	if err != nil {
		return err
	}
	failures := make([]error, len(sources))
	var pending sync.WaitGroup
	for offset, source := range sources {
		if !source.Enabled {
			continue
		}
		pending.Go(func() {
			failures[offset] = c.Refresh(ctx, source.ID)
		})
	}
	pending.Wait()
	return errors.Join(failures...)
}

// Refreshing reports whether a source currently owns an admitted refresh worker.
func (c *CatalogService) Refreshing(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flights[id] != nil
}

// beginRefresh admits one source worker or returns the existing completion owner.
// Worker registration occurs under the shutdown lock. The worker publishes its
// result before closing done, so waiting readers observe one consistent completion.
//
// Configuration mutation is serialized inside the worker rather than the caller,
// allowing canceled callers to release their HTTP request while cache work continues.
// Shutdown prevents any later registration before waiting begins. Completed flights
// leave the map only after their source operation returns, so overlapping requests
// cannot accidentally start a second transfer for the same catalog.
func (c *CatalogService) beginRefresh(id int64) (*indexRefresh, error) {
	if id <= 0 {
		return nil, ErrIndexNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.options.Enabled || c.stopped {
		return nil, ErrPluginsDisabled
	}
	if flight := c.flights[id]; flight != nil {
		return flight, nil
	}
	flight := &indexRefresh{done: make(chan struct{})}
	c.flights[id] = flight
	c.workers.Go(func() {
		err := c.withSource(c.ctx, id, func() error { return c.refreshSource(c.ctx, id) })
		if err != nil && c.ctx.Err() == nil {
			c.logger.Warn("Plugin index refresh failed", "index_id", id, "error", indexFailureMessage(err))
		}
		c.mu.Lock()
		flight.err = err
		delete(c.flights, id)
		close(flight.done)
		c.mu.Unlock()
	})
	return flight, nil
}

// refreshSource validates original bytes before replacing a source's accepted cache.
// Callers hold the source mutation gate. Signature or schema failure preserves
// the prior document and timestamp while recording the failed attempt's diagnostics.
//
// Fetch success alone is insufficient because authenticated bytes can still contain
// an unsupported or malformed catalog. Host-aware parsing decides that boundary before
// persistence. Cancellation is checked after transport so a stopped worker cannot
// publish a late result. Persistence errors remain failures even when verification
// succeeded, and never imply that a new cache is durably available.
func (c *CatalogService) refreshSource(ctx context.Context, id int64) error {
	source, err := c.indexes.GetIndex(ctx, id)
	if err != nil {
		return err
	}
	if !source.Enabled {
		return ErrIndexNotFound
	}
	fetched, failure := c.fetcher.Fetch(ctx, IndexFetchRequest{URL: source.URL, PublicKey: source.PublicKey, Builtin: source.Builtin})
	if err := ctx.Err(); err != nil {
		return err
	}
	if failure == nil {
		if _, err := index.ParseForHost(fetched.Document, c.options.Host.Version); err != nil {
			failure = ErrIndexInvalid.WithCause(err)
		}
		if fetched.SignatureStatus != SignatureOfficial && fetched.SignatureStatus != SignatureCustom && fetched.SignatureStatus != SignatureUnsigned {
			failure = ErrIndexSignatureInvalid
		}
	}
	if fetched.SignatureStatus != "" {
		source.SignatureStatus = fetched.SignatureStatus
	}
	if failure != nil {
		source.LastError = indexFailureMessage(failure)
	} else {
		// Publish document, timestamp, and cleared diagnostics in one source write;
		// successful refreshes expose a complete accepted generation rather than a partially
		// replaced cache. Failed attempts intentionally retain the earlier document.
		now := c.options.Now()
		source.CachedDocument = bytes.Clone(fetched.Document)
		source.CachedAt = &now
		source.LastError = ""
	}
	if err := c.indexes.SaveIndex(ctx, source); err != nil {
		return errors.Join(failure, err)
	}
	return failure
}

// withSource serializes source mutation with cancellation-aware gate admission.
// ID zero owns registration changes; positive IDs own existing sources. Callers
// acquiring both gates always acquire zero first, and refresh workers need only their source gate.
//
// Gate creation shares the shutdown lock, but network and storage operations run
// after releasing it so independent sources can progress. Waiting callers may leave
// through their context without entering the operation. A second cancellation check
// after admission prevents a ready gate from winning over an already canceled caller.
func (c *CatalogService) withSource(ctx context.Context, id int64, action func() error) error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return ErrPluginsDisabled
	}
	gate := c.gates[id]
	if gate == nil {
		gate = make(chan struct{}, 1)
		c.gates[id] = gate
	}
	c.mu.Unlock()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return ErrPluginsDisabled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.ctx.Err() != nil {
		return ErrPluginsDisabled
	}
	return action()
}

// indexFailureMessage retains a bounded UTF-8 diagnostic for source rows and logs.
// Long remote parser messages cannot dominate the management response. Truncation
// preserves complete characters and marks the omitted suffix within the same byte budget.
//
// Application errors carry transport categories and metadata in their diagnostic
// serialization. Administrator source rows need the human message and useful cause,
// so those parts are projected before applying the shared byte bound. Wrapped
// context remains visible around the projection, while nested application causes
// receive the same treatment instead of exposing another serialization frame.
func indexFailureMessage(err error) string {
	message := fmt.Sprint(err)
	if application, ok := errors.AsType[*apperror.ApplicationError](err); ok {
		readable := application.Message
		if cause := application.Unwrap(); cause != nil {
			readable += ": " + indexFailureMessage(cause)
		}
		// Source diagnostics retain the useful cause while removing internal category,
		// metadata-map, and error-serialization framing from the administrator's status column.
		message = strings.Replace(message, application.Error(), readable, 1)
	}
	message = strings.ToValidUTF8(message, "�")
	// Source diagnostics share the task-result one-kilobyte presentation bound.
	if len(message) <= 1024 {
		return message
	}
	end := 1021
	for !utf8.ValidString(message[:end]) {
		end--
	}
	return message[:end] + "..."
}
