package plugin

import (
	"context"
	"errors"
	"io"
)

// data.go coordinates durable plugin directories with installation ownership.
// Files survive ordinary uninstall and remain discoverable by their plugin ID.
// Backup and restore require disabled or uninstalled plugins, and use the same
// per-plugin lock as installation changes so execution cannot restart midway.
//
// The file store owns directory validation and archive integrity. Management
// owns admission and callback draining before accessing stopped business data.
// Database KV and local files have separate commits: a failed file purge remains
// discoverable and can be retried after the installation row has been removed.
// External databases and object stores retain their operator-managed lifecycle.

const (
	// DataMaxArchiveBytes bounds temporary disk use for compressed archives and expanded business files.
	DataMaxArchiveBytes = int64(1 << 30)
)

// DataUsage describes local business files without exposing their absolute paths.
type DataUsage struct {
	PluginID string `json:"plugin_id"` // publisher-qualified data owner
	Bytes    int64  `json:"bytes"`     // regular file bytes
	Files    int64  `json:"files"`     // regular file count
	// Installed reports record presence; Enabled follows that record's desired
	// execution state and stays false when the installation has been removed, even
	// while local business files remain available for reinstallation.
	Installed bool `json:"installed"` // installation record currently exists
	Enabled   bool `json:"enabled"`   // installation currently requests execution
}

// DataArchive owns a completed immutable backup and its temporary file cleanup.
type DataArchive struct {
	// Reader owns the immutable snapshot and its temporary file. Callers close
	// it after delivery, including interrupted responses, to release that storage.
	Reader io.ReadCloser
	Bytes  int64 // compressed archive byte length
}

// DataStorage manages plugin-owned directories and validated offline file archives.
// @project-doc docs/architecture/plugin_system.md#persistent_data
type DataStorage interface {
	// Directory creates the stable directory for id and returns its absolute path.
	// Callers serialize creation with purge and restore for this plugin. Existing
	// data remains available across package replacements and ordinary uninstallation.
	Directory(context.Context, string) (string, error)
	// List measures retained directories without following links into other owners.
	List(context.Context) ([]DataUsage, error)
	// Remove deletes one owner's files after callers drain all plugin work.
	// Missing directories succeed, allowing cleanup to resume after partial failure.
	// Retained host financial and notification ledgers belong to their existing stores.
	Remove(context.Context, string) error
	// Backup snapshots stopped business files into a checksummed portable archive.
	// Callers hold the plugin management lock and close the returned reader after
	// use, releasing its temporary storage even when response delivery fails.
	Backup(context.Context, string) (*DataArchive, error)
	// Restore validates a complete archive before publishing an absent or empty directory.
	// Nonempty destinations are rejected. Callers keep the plugin stopped and hold
	// its management lock throughout validation and publication of restored files.
	Restore(context.Context, string, io.Reader) error
}

// ListData returns durable directory usage including uninstalled plugin owners.
// Sizes are observations rather than quotas; running plugins may change them.
// Each record is annotated from current installation intent without reading secrets.
func (s *Service) ListData(ctx context.Context) ([]DataUsage, error) {
	items, err := s.manager.data.List(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := s.manager.store.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Installation, len(installed))
	for _, item := range installed {
		byID[item.PluginID] = item
	}
	for i := range items {
		item, ok := byID[items[i].PluginID]
		items[i].Installed = ok
		items[i].Enabled = ok && item.Enabled
	}
	return items, nil
}

// PurgeData clears local files and KV for an uninstalled plugin identity.
// An installed owner must be removed first. The management lock prevents a
// concurrent reinstall from acquiring the directory while deletion is in progress.
//
// Host KV commits before local files are removed, because those stores cannot
// share a transaction. A filesystem failure leaves the owner discoverable for
// retry; successful cleanup also removes any package state left behind by an
// earlier partially completed uninstall. Financial and delivery ledger history remains.
func (s *Service) PurgeData(ctx context.Context, id string) error {
	return s.manager.locked(ctx, id, func() error {
		_, err := s.manager.store.Get(ctx, id)
		if err == nil {
			return ErrPluginAlreadyInstalled
		}
		if !errors.Is(err, ErrPluginNotFound) {
			return err
		}
		if err := s.manager.drainLocked(ctx, id); err != nil {
			return err
		}
		// Delete host KV before filesystem cleanup so failed file removal remains
		// visible and retryable without allowing a new executable generation between steps.
		if err := s.manager.store.Delete(ctx, id, true); err != nil {
			return err
		}
		if err := s.manager.data.Remove(ctx, id); err != nil {
			return err
		}
		return s.manager.syncLocked(ctx, id)
	})
}

// BackupData produces a stable snapshot of a disabled or uninstalled owner's files.
// The returned archive is independent of later execution and belongs to the caller.
// Installed configuration, host KV, ledgers, and external stores require their respective backups.
//
// Stopped intent is checked while holding the same owner lock used by enable and
// reinstall, then actual process and callback completion is awaited. Only snapshot
// creation holds that lock; delivery reads an immutable temporary file, allowing
// later execution without making the downloaded bytes inconsistent.
func (s *Service) BackupData(ctx context.Context, id string) (*DataArchive, error) {
	var archive *DataArchive
	err := s.manager.locked(ctx, id, func() error {
		if err := s.dataStopped(ctx, id); err != nil {
			return err
		}
		var err error
		archive, err = s.manager.data.Backup(ctx, id)
		return err
	})
	return archive, err
}

// RestoreData publishes validated business files while keeping their owner stopped.
// The archive must match id and the destination must be absent or empty. Current
// installation configuration and permissions remain governed by ordinary administration.
func (s *Service) RestoreData(ctx context.Context, id string, reader io.Reader) error {
	return s.manager.locked(ctx, id, func() error {
		if err := s.dataStopped(ctx, id); err != nil {
			return err
		}
		return s.manager.data.Restore(ctx, id, reader)
	})
}

// dataStopped checks disabled intent and waits for actual executable callback completion.
// It runs under the owner's management lock. Drain failure leaves files untouched,
// and an enabled installation requires an administrator to disable it before retrying.
func (s *Service) dataStopped(ctx context.Context, id string) error {
	item, err := s.manager.store.Get(ctx, id)
	if err != nil && !errors.Is(err, ErrPluginNotFound) {
		return err
	}
	if item != nil && item.Enabled {
		return ErrDataRequiresStop
	}
	return s.manager.drainLocked(ctx, id)
}
