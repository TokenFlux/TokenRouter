package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// service_versions.go applies reviewed releases and complete rollback snapshots.
// Updates preserve configuration under the target schema, retain the current
// snapshot as previous, and wait for enabled code to become ready. A failed
// candidate restores the entire pre-update record, including its older previous
// snapshot, without changing installation-level limits or desired enablement.
//
// Original package versions stay retained through readiness observation, so failure
// compensation can still use an uploaded previous package that cannot be downloaded
// again. Archive digest paths keep same-version replacement bytes separate from retained code.
// Manual rollback swaps accepted snapshots and lets normal reconciliation expose
// missing-package or startup failures, allowing uploaded-package repair to target
// the restored current version while retaining its original configuration.

// UpdateInput selects an indexed version or uploaded archive and confirms its capability set.
type UpdateInput struct {
	Version           string   `json:"version,omitempty"`             // explicit indexed release selection
	UploadToken       string   `json:"upload_token,omitempty"`        // inspected uploaded release reference
	AcceptPermissions []string `json:"accept_permissions"`            // reviewed target capability names
	IgnoreHostVersion bool     `json:"ignore_host_version,omitempty"` // bypass only gateway version bounds
	Force             bool     `json:"force,omitempty"`               // allow same-version package replacement
}

// resolveInstall converts one unambiguous source selection into accepted release metadata.
// Upload selection cannot also name an index or release. Indexed selection requires
// an exact version, so background catalog refresh cannot substitute unreviewed code.
func (s *Service) resolveInstall(ctx context.Context, input InstallInput) (VersionSnapshot, error) {
	switch input.Source {
	case SourceUpload:
		if input.UploadToken == "" || input.IndexID != 0 || input.PluginID != "" || input.Version != "" || s.uploads == nil {
			return VersionSnapshot{}, ErrPackageInvalid
		}
		upload, err := s.uploads.ResolveUpload(ctx, input.UploadToken)
		if err != nil {
			return VersionSnapshot{}, err
		}
		return upload.Snapshot, nil
	case SourceIndex:
		if input.UploadToken != "" || s.releases == nil {
			return VersionSnapshot{}, ErrIndexInvalid
		}
		return s.releases.Resolve(ctx, input)
	default:
		return VersionSnapshot{}, ErrIndexInvalid
	}
}

// Update publishes a reviewed target and restores original intent when enabled startup fails.
// The plugin lock spans fresh source resolution, persistence, readiness, and compensation.
// Disabled installations replace metadata without starting code, and development records use directory reload instead.
//
// Target files are verified before changing durable intent. Original current and
// previous directories remain retained until the new generation is ready, preserving
// rollback even when an older uploaded package is no longer downloadable. A failed
// readiness observation uses a bounded context independent of the departing HTTP
// caller to restore the complete original record. Recovery failures remain explicit
// in error metadata rather than claiming the old process resumed successfully.
func (s *Service) Update(ctx context.Context, id string, input UpdateInput, actor int64) (*ChangeResult, error) {
	var result *ChangeResult
	err := s.manager.locked(ctx, id, func() error {
		if !s.manager.options.Enabled {
			return ErrPluginsDisabled
		}
		original, err := s.manager.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if original.Source == SourceDev {
			return ErrPluginAlreadyInstalled
		}
		selection := InstallInput{Source: SourceUpload, UploadToken: input.UploadToken}
		if input.UploadToken == "" {
			if original.Source != SourceIndex || original.IndexID == nil {
				return ErrIndexInvalid
			}
			selection = InstallInput{Source: SourceIndex, IndexID: *original.IndexID, PluginID: id, Version: input.Version}
		} else if input.Version != "" {
			return ErrPackageInvalid
		}
		selection.IgnoreHostVersion = input.IgnoreHostVersion
		selection.Force = input.Force
		target, err := s.resolveInstall(ctx, selection)
		if err != nil {
			return err
		}
		if target.Manifest == nil || target.Manifest.ID != id {
			return ErrPackageInvalid
		}
		if target.Version == original.Version && !input.Force {
			return ErrPluginAlreadyInstalled
		}
		target.IgnoreHostVersion = input.IgnoreHostVersion
		target.GrantedPermissions = slices.Clone(input.AcceptPermissions)
		result, err = s.replaceLocked(ctx, original, target, input.UploadToken, actor)
		return err
	})
	return result, err
}

// replaceLocked applies a verified replacement under the caller's plugin mutation lock.
// Existing configuration, limits, enabled intent, and business data remain owned by
// the installation. Development directories cannot be replaced by managed packages.
//
// Candidate archives have distinct digest paths, even when versions are equal, so
// verification cannot overwrite current or rollback bytes. Both original snapshots
// stay retained through readiness observation. Failed activation restores the entire
// original record with an independent recovery budget, preserving its older rollback
// target and the version-check exception previously accepted for that target.
func (s *Service) replaceLocked(ctx context.Context, original *Installation, target VersionSnapshot, token string, actor int64) (*ChangeResult, error) {
	if original.Source == SourceDev {
		return nil, ErrPluginAlreadyInstalled
	}
	candidate := *original
	candidate.VersionSnapshot = target
	candidate.GrantedPermissions = slices.Clone(target.GrantedPermissions)
	previous := original.VersionSnapshot
	candidate.Previous = &previous
	candidate.UpdatedBy = &actor
	if err := s.manager.validate(&candidate); err != nil {
		return nil, permissionsForUpdate(err, original.GrantedPermissions, candidate.Manifest.Permissions)
	}
	if err := s.manager.configuration.Migrate(&original.VersionSnapshot, &candidate.VersionSnapshot, candidate.Enabled); err != nil {
		return nil, err
	}
	if _, err := s.manager.packages.Ensure(ctx, packageReference(&candidate, token)); err != nil {
		return nil, err
	}
	// Persist only after target verification; retain both original generations until
	// readiness succeeds so compensation never depends on a deleted uploaded package.
	if err := s.manager.store.Save(ctx, &candidate); err != nil {
		cleanup := s.manager.prune(context.WithoutCancel(ctx), original)
		return nil, errors.Join(err, cleanup)
	}
	retain := []PackageRef{packageReference(original, "")}
	if original.Previous != nil {
		retain = append(retain, packageReference(&Installation{PluginID: original.PluginID, VersionSnapshot: *original.Previous}, ""))
	}
	// A forced reinstall of identical bytes still reloads the running process.
	// Draining before reconciliation prevents its same-release optimization from keeping that generation alive.
	var applyError error
	if sameRelease(original, &candidate) {
		applyError = s.manager.drainLocked(ctx, original.PluginID)
	}
	if applyError == nil {
		applyError = s.manager.syncLocked(ctx, original.PluginID, retain...)
	}
	if applyError == nil && candidate.Enabled {
		s.waitReady(ctx, original.PluginID, 30*time.Second)
	}
	status := s.manager.Status(original.PluginID)
	ready := !candidate.Enabled || status.State == StateRunning || status.State == StateStatic
	if applyError == nil && ready {
		if err := s.manager.prune(ctx, &candidate); err != nil {
			return nil, err
		}
		return s.result(ctx, original.PluginID)
	}
	return nil, s.restoreUpdate(ctx, original, candidate.Version, applyError, status)
}

// Rollback swaps complete accepted snapshots while retaining installation-level policy.
// The selected manifest must remain compatible with this host. Missing package
// files become a failed restored installation, allowing same-version upload repair afterward.
//
// The plugin mutation lock spans the fresh read, complete snapshot exchange, and
// runtime reconciliation. Configuration, encrypted secrets, source identity, and
// grants move together, while enablement and quotas belong to the installation.
// Validation precedes persistence; a later process failure remains visible on the
// selected snapshot so administrators can repair or exchange it again without
// losing the other previously accepted release.
func (s *Service) Rollback(ctx context.Context, id string, actor int64) (*ChangeResult, error) {
	var result *ChangeResult
	err := s.manager.locked(ctx, id, func() error {
		if !s.manager.options.Enabled {
			return ErrPluginsDisabled
		}
		item, err := s.manager.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if item.Source == SourceDev || item.Previous == nil {
			return ErrPluginNotFound
		}
		current := item.VersionSnapshot
		item.VersionSnapshot = *item.Previous
		item.Previous = &current
		item.UpdatedBy = &actor
		if err := s.manager.validate(item); err != nil {
			return err
		}
		// Save the complete exchange before reconciliation, keeping both accepted snapshots
		// available even when the restored package is missing or its process cannot start.
		if err := s.manager.store.Save(ctx, item); err != nil {
			return err
		}
		if err := s.manager.syncLocked(ctx, id); err == nil && item.Enabled {
			s.waitReady(ctx, id, 20*time.Second)
		}
		result, err = s.result(ctx, id)
		return err
	})
	return result, err
}

// restoreUpdate records candidate diagnostics and compensates with the complete original installation.
// Recovery uses an independent thirty-second budget after caller cancellation.
// The returned update error includes the failed candidate and at most fifty retained log lines.
//
// Original durable intent is restored before reconciliation attempts to drain the
// failed generation. This preserves the complete previous snapshot even when draining
// or restarting cannot finish immediately. Only a successful restoration and observed
// old readiness produce restored_version metadata; other outcomes retain rollback_error
// so clients do not mistake a saved version number for resumed execution.
func (s *Service) restoreUpdate(ctx context.Context, original *Installation, candidate string, applyError error, status Status) error {
	message := status.LastError
	if message == "" {
		if applyError != nil {
			message = applyError.Error()
		} else {
			message = "new version did not become ready before the update deadline"
		}
	}
	metadata := map[string]string{"version": candidate, "start_error": indexFailureMessage(errors.New(message))}
	if s.logs != nil {
		lines := s.logs.Logs(original.PluginID)
		// Failure responses retain only the last fifty lines, matching the update
		// contract while leaving the full bounded process buffer to the log endpoint.
		if len(lines) > 50 {
			lines = lines[len(lines)-50:]
		}
		data, _ := json.Marshal(lines)
		metadata["logs"] = string(data)
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	// Restore durable intent before draining the failed candidate; interrupted process
	// recovery must not leave the database claiming that unready code was accepted.
	err := s.manager.store.Save(recovery, original)
	if err == nil {
		err = s.manager.syncLocked(recovery, original.PluginID)
		if err == nil && original.Enabled {
			s.waitReady(recovery, original.PluginID, 20*time.Second)
			state := s.manager.Status(original.PluginID)
			if state.State != StateRunning && state.State != StateStatic {
				err = fmt.Errorf("restored version is %s: %s", state.State, state.LastError)
			}
		}
	}
	if err != nil {
		metadata["rollback_error"] = indexFailureMessage(err)
	} else {
		metadata["restored_version"] = original.Version
	}
	return ErrUpdateFailed.WithMetadata(metadata).WithCause(errors.Join(applyError, err))
}

// permissionsForUpdate adds the newly requested capability subset to existing confirmation errors.
// The complete target set remains required. Unrelated validation failures retain
// their original reason and metadata without being misclassified as missing approval.
func permissionsForUpdate(err error, previous, requested []string) error {
	if !errors.Is(err, ErrPermissionsNotAccepted) {
		return err
	}
	added := make([]string, 0)
	for _, permission := range requested {
		if !slices.Contains(previous, permission) {
			added = append(added, permission)
		}
	}
	all, _ := json.Marshal(requested)
	newPermissions, _ := json.Marshal(added)
	return ErrPermissionsNotAccepted.WithMetadata(map[string]string{"permissions": string(all), "new_permissions": string(newPermissions)})
}
