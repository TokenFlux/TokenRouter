package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"
)

// service.go coordinates administrator installation changes under the manager's
// per-plugin lock. Package verification precedes persistence, and configuration
// validation precedes enabling. Once an installation is saved, runtime failures
// remain visible on that record so administrators can inspect, repair, or remove
// it without losing the accepted source, policy, or configuration.
//
// Uninstall closes admission and drains both native requests and host callbacks
// before deleting installation intent and optional KV in one store transaction.
// Failure to drain preserves durable state and supports retry. Same-version
// uploaded repair preserves configuration and grants, but only replaces missing
// or invalid managed files with a package bearing the original manifest digest.

// InstallInput names one indexed or uploaded release and its explicit execution authorization.
type InstallInput struct {
	IndexID           int64    `json:"index_id,omitempty"`            // selected catalog registration identifier
	PluginID          string   `json:"plugin_id,omitempty"`           // indexed plugin identity
	Version           string   `json:"version,omitempty"`             // explicitly selected indexed version
	Source            Source   `json:"source"`                        // requested package distribution source
	UploadToken       string   `json:"upload_token,omitempty"`        // inspected staged archive handle
	AcceptPermissions []string `json:"accept_permissions"`            // explicitly accepted capability names
	Enable            bool     `json:"enable"`                        // requested initial runtime availability
	IgnoreHostVersion bool     `json:"ignore_host_version,omitempty"` // bypass only gateway version bounds
	Force             bool     `json:"force,omitempty"`               // replace an existing durable installation
}

// ChangeResult reports accepted durable state and its observed runtime outcome.
type ChangeResult struct {
	Installation       *Installation // independently read installation record
	Status             Status        // observed local runtime state
	NeedsConfiguration bool          // required configuration remains incomplete
	Repaired           bool          // existing upload installation was repaired
}

// Service executes administration through one shared reconciliation and storage boundary.
type Service struct {
	manager  *Manager        // serialized local reconciliation owner
	uploads  Uploads         // archive preview and token resolution
	releases ReleaseResolver // source-qualified indexed release selection
	logs     LogReader       // retained startup diagnostics for failed updates
}

// NewService constructs administration around shared reconciliation, release selection, and diagnostics.
// A nil release resolver supports upload-only callers; indexed requests then fail.
// A nil log reader leaves failure metadata without lines, while runtime status remains available.
func NewService(manager *Manager, uploads Uploads, releases ReleaseResolver, logs LogReader) *Service {
	return &Service{manager: manager, uploads: uploads, releases: releases, logs: logs}
}

// Preview validates an archive without persisting installation intent or executing code.
func (s *Service) Preview(ctx context.Context, reader io.Reader) (*Upload, error) {
	if !s.manager.options.Enabled {
		return nil, ErrPluginsDisabled
	}
	return s.uploads.Preview(ctx, reader)
}

// List returns durable and development installation records for administrative presentation.
func (s *Service) List(ctx context.Context) ([]Installation, error) {
	return s.manager.store.List(ctx)
}

// Get retrieves installation intent independently of whether its process currently runs.
func (s *Service) Get(ctx context.Context, id string) (*Installation, error) {
	return s.manager.store.Get(ctx, id)
}

// Install verifies a selected release before saving its accepted installation intent.
// Missing required configuration forces initial disablement and is reported in
// the result. Startup failure after persistence remains an installed record with
// diagnostic status, rather than returning an ambiguous failed creation response.
//
// Resolution happens before locking to identify the plugin, then compatibility,
// permissions, and existing state are checked under that plugin's mutation lock.
// Explicit force requests reuse update recovery, preserving configuration and data.
// Other same-version uploads enter the restricted repair path. A first install
// publishes files before its database row; a failed database save removes only
// those newly owned files because no generation has yet been admitted.
func (s *Service) Install(ctx context.Context, input InstallInput, actor int64) (*ChangeResult, error) {
	if !s.manager.options.Enabled {
		return nil, ErrPluginsDisabled
	}
	snapshot, err := s.resolveInstall(ctx, input)
	if err != nil {
		return nil, err
	}
	item := &Installation{PluginID: snapshot.Manifest.ID, VersionSnapshot: snapshot, Enabled: input.Enable, UpdatedBy: &actor}
	item.GrantedPermissions = slices.Clone(input.AcceptPermissions)
	item.IgnoreHostVersion = input.IgnoreHostVersion
	var result *ChangeResult
	err = s.manager.locked(ctx, item.PluginID, func() error {
		if err := s.manager.validate(item); err != nil {
			return err
		}
		old, err := s.manager.store.Get(ctx, item.PluginID)
		if err == nil {
			if input.Force {
				var replaceErr error
				result, replaceErr = s.replaceLocked(ctx, old, item.VersionSnapshot, input.UploadToken, actor)
				return replaceErr
			}
			var repairErr error
			result, repairErr = s.repairLocked(ctx, old, item, input.UploadToken, actor)
			return repairErr
		}
		if !errors.Is(err, ErrPluginNotFound) {
			return err
		}
		if err := s.manager.configuration.Set(&item.VersionSnapshot, nil, false); err != nil {
			return err
		}
		if _, err := s.manager.configuration.Plain(&item.VersionSnapshot); err != nil {
			item.Enabled = false
		}
		if _, err := s.manager.packages.Ensure(ctx, packageReference(item, input.UploadToken)); err != nil {
			return err
		}
		if err := s.manager.store.Save(ctx, item); err != nil {
			// No installation or process exists yet, so failed persistence can remove this newly verified package without touching another generation's files.
			cleanupErr := s.manager.packages.Remove(context.WithoutCancel(ctx), item.PluginID)
			return errors.Join(err, cleanupErr)
		}
		if err := s.manager.syncLocked(ctx, item.PluginID); err == nil && item.Enabled {
			s.waitReady(ctx, item.PluginID, 20*time.Second)
		}
		result, err = s.result(ctx, item.PluginID)
		return err
	})
	return result, err
}

// repairLocked restores an uploaded installation whose accepted files are missing or damaged.
// The original manifest digest, configuration, grants, limits, and desired enabled
// state survive repair. Complete file digests distinguish damage from a healthy
// installed package, which cannot be replaced through this same-version path;
// ordinary release changes belong to the update workflow.
//
// The old generation is drained before publishing replacement files, including on
// platforms that lock executable files. Only the accepted archive identity changes
// after successful extraction. If persistence fails, the installation remains
// stopped and can be repaired again using the same uploaded token and manifest.
func (s *Service) repairLocked(ctx context.Context, old, candidate *Installation, token string, actor int64) (*ChangeResult, error) {
	if old.Source != SourceUpload || old.Version != candidate.Version || old.ManifestSHA256 != candidate.ManifestSHA256 {
		return nil, ErrPluginAlreadyInstalled
	}
	if _, err := s.manager.packages.Ensure(ctx, packageReference(old, "")); err == nil {
		return nil, ErrPluginAlreadyInstalled
	} else if !errors.Is(err, ErrPackageMissing) {
		return nil, err
	}
	if err := s.manager.drainLocked(ctx, old.PluginID); err != nil {
		return nil, err
	}
	old.Artifacts = candidate.Artifacts
	old.UpdatedBy = &actor
	if _, err := s.manager.packages.Ensure(ctx, packageReference(old, token)); err != nil {
		return nil, err
	}
	if err := s.manager.store.Save(ctx, old); err != nil {
		return nil, err
	}
	// Invalidate even an equal-version snapshot so the new marker's binary and asset identities reach process and HTTP consumers together.
	s.manager.mu.Lock()
	delete(s.manager.loaded, old.PluginID)
	s.manager.mu.Unlock()
	if err := s.manager.syncLocked(ctx, old.PluginID); err == nil && old.Enabled {
		s.waitReady(ctx, old.PluginID, 20*time.Second)
	}
	result, err := s.result(ctx, old.PluginID)
	if result != nil {
		result.Repaired = true
	}
	return result, err
}

// Enable validates complete configuration before persisting desired runtime availability.
// The saved installation survives later process failure for operator inspection.
// Global plugin disablement rejects this action without changing installation intent.
// Validation and persistence share the plugin lock, preventing a queued config
// mutation from changing required fields between them. Runtime synchronization
// follows the committed record, and bounded readiness observation reports its
// actual outcome without undoing enabled intent when a process needs operator repair.
func (s *Service) Enable(ctx context.Context, id string, actor int64) (*ChangeResult, error) {
	var result *ChangeResult
	err := s.manager.locked(ctx, id, func() error {
		if !s.manager.options.Enabled {
			return ErrPluginsDisabled
		}
		item, err := s.manager.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := s.manager.validate(item); err != nil {
			return err
		}
		if _, err := s.manager.configuration.Plain(&item.VersionSnapshot); err != nil {
			return err
		}
		item.Enabled = true
		item.UpdatedBy = &actor
		if err := s.manager.store.Save(ctx, item); err != nil {
			return err
		}
		if err := s.manager.syncLocked(ctx, id); err == nil {
			s.waitReady(ctx, id, 20*time.Second)
		}
		result, err = s.result(ctx, id)
		return err
	})
	return result, err
}

// Disable persists disabled intent and drains the installation's admitted work.
// The record remains disabled if draining reaches its deadline. A later retry
// waits for the same outstanding callbacks before reporting completed shutdown.
// Persistence happens before draining so a host restart cannot revive an
// installation whose administrator has already disabled it. Successful draining
// clears an earlier management failure even when missing package files prevented
// an asset snapshot from loading; disabling does not require those files.
func (s *Service) Disable(ctx context.Context, id string, actor int64) (*ChangeResult, error) {
	var result *ChangeResult
	err := s.manager.locked(ctx, id, func() error {
		item, err := s.manager.store.Get(ctx, id)
		if err != nil {
			return err
		}
		item.Enabled = false
		item.UpdatedBy = &actor
		if err := s.manager.store.Save(ctx, item); err != nil {
			return err
		}
		// Publish the saved disabled intent before draining so UI discovery and new leases cannot retain an enabled view after a shutdown timeout.
		s.manager.mu.Lock()
		if loaded, ok := s.manager.loaded[id]; ok {
			loaded.Installation = item
			s.manager.loaded[id] = loaded
		}
		s.manager.mu.Unlock()
		if err := s.manager.drainLocked(ctx, id); err != nil {
			s.manager.recordFailure(id, err)
			return err
		}
		if loaded, ok := s.manager.Snapshot(id); ok {
			loaded.Installation = item
			if err := s.manager.publish(id, loaded, Status{State: StateStopped, Since: time.Now()}); err != nil {
				return err
			}
		} else {
			// A package-recovery failure may never publish assets; disabling still clears its previous failure after the supervisor confirms completed draining.
			s.manager.mu.Lock()
			s.manager.observations[id] = Status{State: StateStopped, Since: time.Now()}
			s.manager.blocked[id] = false
			s.manager.mu.Unlock()
		}
		result, err = s.result(ctx, id)
		return err
	})
	return result, err
}

// Uninstall drains admitted work before transactionally deleting installation intent and optional KV.
// Drain failure leaves the installation intact with request admission closed for
// retry. Ledger history always remains, and local files are removed after commit.
//
// The same plugin lock spans admission closure, callback completion, store deletion,
// and local cleanup. An arriving enable or reinstall request therefore cannot
// create a generation between drain and purge. Missing records still clean managed
// files, supporting retries after a committed deletion whose filesystem cleanup
// failed. PostgreSQL deletion owns the atomic installation and KV transaction.
func (s *Service) Uninstall(ctx context.Context, id string, purge bool) error {
	return s.manager.locked(ctx, id, func() error {
		if err := s.manager.drainLocked(ctx, id); err != nil {
			s.manager.recordFailure(id, err)
			return err
		}
		if err := s.manager.store.Delete(ctx, id, purge); err != nil {
			return err
		}
		return s.manager.syncLocked(ctx, id)
	})
}

// Config returns ordinary settings and secret presence markers for administration.
func (s *Service) Config(ctx context.Context, id string) (map[string]any, error) {
	item, err := s.manager.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.manager.configuration.Public(&item.VersionSnapshot)
}

// SetConfig validates replacement settings before persisting and notifying a running plugin.
// Omitted secrets retain their values; empty secrets clear them. Enabled plugins
// require complete values, while disabled installations may save incomplete configuration.
// The plugin lock spans reading, validation, persistence, and runtime notification,
// preventing two administrators from merging secrets against the same stale record.
// Process callback failures retain the committed configuration for the next startup.
// Storage and schema failures return before any new settings are published.
func (s *Service) SetConfig(ctx context.Context, id string, values map[string]any, actor int64) error {
	return s.manager.locked(ctx, id, func() error {
		item, err := s.manager.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := s.manager.configuration.Set(&item.VersionSnapshot, values, item.Enabled); err != nil {
			return err
		}
		item.UpdatedBy = &actor
		if err := s.manager.store.Save(ctx, item); err != nil {
			return err
		}
		return s.manager.syncLocked(ctx, id)
	})
}

// Limits returns the complete policy after applying defaults to omitted stored fields.
func (s *Service) Limits(ctx context.Context, id string) (Limits, error) {
	item, err := s.manager.store.Get(ctx, id)
	if err != nil {
		return Limits{}, err
	}
	return ParseLimits(item.Limits)
}

// SetLimits replaces a validated capability policy and publishes it to local consumers.
// Explicit zeroes remain prohibitions. The policy belongs to the installation
// and does not alter its current or previous version configuration snapshots.
// Validation occurs before acquiring the plugin lock because the replacement
// policy is self-contained. The current installation is read only after admission,
// so queued version or configuration changes survive this update. Persistence
// precedes publication to capability readers, keeping startup and runtime policy consistent.
func (s *Service) SetLimits(ctx context.Context, id string, data []byte, actor int64) error {
	limits, err := ParseLimits(data)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(limits)
	if err != nil {
		return err
	}
	return s.manager.locked(ctx, id, func() error {
		item, err := s.manager.store.Get(ctx, id)
		if err != nil {
			return err
		}
		item.Limits = encoded
		item.UpdatedBy = &actor
		if err := s.manager.store.Save(ctx, item); err != nil {
			return err
		}
		return s.manager.syncLocked(ctx, id)
	})
}

// result reads durable intent and combines it with current local startup diagnostics.
// Missing or unreadable required configuration sets NeedsConfiguration without losing
// the installed record. Other storage failures still prevent a successful response.
func (s *Service) result(ctx context.Context, id string) (*ChangeResult, error) {
	item, err := s.manager.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	_, configErr := s.manager.configuration.Plain(&item.VersionSnapshot)
	return &ChangeResult{Installation: item, Status: s.manager.Status(id), NeedsConfiguration: configErr != nil}, nil
}

// waitReady observes startup until running, a known failure, or the bounded deadline.
// Cancellation ends waiting without disabling a persisted installation. The supervisor
// retains ownership of asynchronous startup and its normal crash recovery policy.
// A known backoff or crash-loop state ends observation immediately so the caller
// can return actionable diagnostics. If the deadline expires while startup is
// still progressing, the later response reports that observed state; this wait
// never claims readiness or cancels the independently owned process generation.
func (s *Service) waitReady(ctx context.Context, id string, budget time.Duration) {
	wait, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch s.manager.Status(id).State {
		case StateRunning, StateStatic, StateFailed, StateBackoff, StateCrashLoop:
			return
		}
		select {
		case <-wait.Done():
			return
		case <-ticker.C:
		}
	}
}
