package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// service_versions_test.go exercises accepted release changes through administration.
// Failed startup must preserve both rollback generations and their secret snapshots,
// while successful changes retain installation-level policy and require explicit
// approval before introducing a different capability set.

// versionUploads resolves an already inspected test release without filesystem dependencies.
type versionUploads struct {
	snapshot VersionSnapshot // accepted target release metadata
}

// versionLogs supplies enough failed-generation output to verify response truncation.
type versionLogs struct{}

// Preview rejects unused archive preview requests in release-transition fixtures.
func (versionUploads) Preview(context.Context, io.Reader) (*Upload, error) {
	return nil, ErrPackageInvalid
}

// ResolveUpload returns independently decoded release metadata for the accepted fixture token.
func (f versionUploads) ResolveUpload(context.Context, string) (*Upload, error) {
	data, err := json.Marshal(f.snapshot)
	if err != nil {
		return nil, err
	}
	var snapshot VersionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &Upload{Snapshot: snapshot}, nil
}

// Logs produces sixty ordered lines for the failed update's bounded error metadata.
func (versionLogs) Logs(string) []LogLine {
	lines := make([]LogLine, 60)
	for i := range lines {
		lines[i].Message = fmt.Sprint(i)
	}
	return lines
}

// TestUpdateFailureRestoresEntireHistory starts a rejected candidate after two accepted generations.
// The previous ciphertext, configuration, grants, and limits survive compensation, and older package files remain retained until the failure is decided.
func TestUpdateFailureRestoresEntireHistory(t *testing.T) {
	manager, service, store, processes, packages := managerFixture(t, 2)
	cipher, err := crypto.NewAESEncryptor(make([]byte, 32))
	require.NoError(t, err)
	manager.configuration = NewConfiguration(cipher, true)
	old := managerInstallation(t, "test.update", true)
	old.Manifest.Config.Fields = []manifest.Field{
		{Key: "token", Type: "secret", Label: manifest.LocalizedText{"en": "Token"}},
		{Key: "label", Type: "string", Label: manifest.LocalizedText{"en": "Label"}},
	}
	require.NoError(t, manager.configuration.Set(&old.VersionSnapshot, map[string]any{"token": "old-secret", "label": "current"}, true))
	prior := managerInstallation(t, old.PluginID, true)
	prior.Version = "0.9.0"
	prior.Manifest.Version = prior.Version
	prior.Config = map[string]any{"older": "preserved"}
	prior.SecretConfig = "older-ciphertext"
	old.Previous = &prior.VersionSnapshot
	old.Limits = json.RawMessage(`{"wallet":{"max_credit_per_day":5}}`)
	require.NoError(t, store.Save(t.Context(), old))
	require.NoError(t, manager.Sync(t.Context(), old.PluginID))
	before, err := store.Get(t.Context(), old.PluginID)
	require.NoError(t, err)
	target := managerInstallation(t, old.PluginID, true)
	target.Version = "2.0.0"
	target.Manifest.Version = target.Version
	target.Manifest.Config = old.Manifest.Config
	service.uploads = versionUploads{snapshot: target.VersionSnapshot}
	service.logs = versionLogs{}
	processes.failVersion = target.Version
	packages.pruned = nil
	_, err = service.Update(t.Context(), old.PluginID, UpdateInput{UploadToken: "reviewed"}, 7)
	require.ErrorIs(t, err, ErrUpdateFailed)
	appErr := apperror.FromError(err)
	require.Equal(t, "1.0.0", appErr.Metadata["restored_version"])
	var lines []LogLine
	require.NoError(t, json.Unmarshal([]byte(appErr.Metadata["logs"]), &lines))
	require.Len(t, lines, 50)
	require.Equal(t, "10", lines[0].Message)
	after, err := store.Get(t.Context(), old.PluginID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, StateRunning, manager.Status(old.PluginID).State)
	require.Equal(t, "1.0.0", processes.started[len(processes.started)-1].Version)
	require.NotEmpty(t, packages.pruned)
	for _, retained := range packages.pruned {
		require.Contains(t, retained, "0.9.0")
		require.Contains(t, retained, "1.0.0")
	}
}

// TestUpdateRequiresNewPermissionsBeforePublishing submits a changed capability set without approval.
// No package is verified or durable state replaced until the entire target permission set is explicitly confirmed.
func TestUpdateRequiresNewPermissionsBeforePublishing(t *testing.T) {
	_, service, store, _, packages := managerFixture(t, 2)
	old := managerInstallation(t, "test.update", false)
	require.NoError(t, store.Save(t.Context(), old))
	target := managerInstallation(t, old.PluginID, false)
	target.Version = "2.0.0"
	target.Manifest.Version = target.Version
	target.Manifest.Permissions = []string{"wallet.credit"}
	service.uploads = versionUploads{snapshot: target.VersionSnapshot}
	_, err := service.Update(t.Context(), old.PluginID, UpdateInput{UploadToken: "reviewed"}, 7)
	require.ErrorIs(t, err, ErrPermissionsNotAccepted)
	require.Empty(t, packages.ensured)
	require.Equal(t, `["wallet.credit"]`, apperror.FromError(err).Metadata["new_permissions"])
	result, err := service.Update(t.Context(), old.PluginID, UpdateInput{UploadToken: "reviewed", AcceptPermissions: []string{"wallet.credit"}}, 7)
	require.NoError(t, err)
	require.False(t, result.Installation.Enabled)
	require.Equal(t, "2.0.0", result.Installation.Version)
	require.Equal(t, "1.0.0", result.Installation.Previous.Version)
	require.Equal(t, StateStopped, result.Status.State)
	result, err = service.Rollback(t.Context(), old.PluginID, 8)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", result.Installation.Version)
	require.Empty(t, result.Installation.GrantedPermissions)
	require.Equal(t, []string{"wallet.credit"}, result.Installation.Previous.GrantedPermissions)
	result, err = service.Rollback(t.Context(), old.PluginID, 8)
	require.NoError(t, err)
	require.Equal(t, "2.0.0", result.Installation.Version)
	require.Equal(t, []string{"wallet.credit"}, result.Installation.GrantedPermissions)
}

// TestInstallOverridesPreserveSnapshotsAndEnforceCapabilities repeatedly installs a local build at an unchanged version.
// Version exceptions survive restart and rollback, while permission and API requirements remain mandatory and existing configuration stays intact.
func TestInstallOverridesPreserveSnapshotsAndEnforceCapabilities(t *testing.T) {
	manager, service, store, processes, _ := managerFixture(t, 2)
	old := managerInstallation(t, "test.overwrite", true)
	old.Manifest.Config.Fields = []manifest.Field{{Key: "label", Type: "string", Label: manifest.LocalizedText{"en": "Label"}}}
	old.Config = map[string]any{"label": "keep"}
	old.Limits = json.RawMessage(`{"wallet":{"max_credit_per_day":5}}`)
	require.NoError(t, store.Save(t.Context(), old))
	require.NoError(t, manager.Sync(t.Context(), old.PluginID))
	target := managerInstallation(t, old.PluginID, true)
	target.Manifest.Config = old.Manifest.Config
	target.Manifest.MinHostVersion = "99.0.0"
	target.ManifestSHA256 = "changed-manifest"
	service.uploads = versionUploads{snapshot: target.VersionSnapshot}
	input := InstallInput{Source: SourceUpload, UploadToken: "reviewed", Force: true}
	_, err := service.Install(t.Context(), input, 7)
	require.ErrorIs(t, err, ErrPluginIncompatible)
	input.IgnoreHostVersion = true
	input.Force = false
	_, err = service.Install(t.Context(), input, 7)
	require.ErrorIs(t, err, ErrPluginAlreadyInstalled)
	input.Force = true
	result, err := service.Install(t.Context(), input, 7)
	require.NoError(t, err)
	require.True(t, result.Installation.IgnoreHostVersion)
	require.False(t, result.Installation.Previous.IgnoreHostVersion)
	require.Equal(t, old.Config, result.Installation.Config)
	require.JSONEq(t, string(old.Limits), string(result.Installation.Limits))
	require.True(t, result.Installation.Enabled)
	require.NoError(t, manager.Restart(t.Context(), old.PluginID))
	require.Equal(t, StateRunning, manager.Status(old.PluginID).State)
	result, err = service.Rollback(t.Context(), old.PluginID, 7)
	require.NoError(t, err)
	require.False(t, result.Installation.IgnoreHostVersion)
	result, err = service.Rollback(t.Context(), old.PluginID, 7)
	require.NoError(t, err)
	require.True(t, result.Installation.IgnoreHostVersion)
	started := len(processes.started)
	_, err = service.Install(t.Context(), input, 7)
	require.NoError(t, err)
	require.Greater(t, len(processes.started), started)
	target.Manifest.Permissions = []string{"wallet.credit"}
	service.uploads = versionUploads{snapshot: target.VersionSnapshot}
	_, err = service.Install(t.Context(), input, 7)
	require.ErrorIs(t, err, ErrPermissionsNotAccepted)
	target.Manifest.APIVersion = 2
	service.uploads = versionUploads{snapshot: target.VersionSnapshot}
	input.AcceptPermissions = []string{"wallet.credit"}
	_, err = service.Install(t.Context(), input, 7)
	require.ErrorIs(t, err, ErrPluginIncompatible)
	require.Empty(t, store.deleted)
}

// TestForcedUpdateRestoresSameVersionAfterStartupFailure replaces an accepted build with failing code bearing the same version.
// Automatic recovery restores the original digest, settings, version exception, and older rollback target instead of keeping the failed candidate.
func TestForcedUpdateRestoresSameVersionAfterStartupFailure(t *testing.T) {
	manager, service, store, processes, _ := managerFixture(t, 2)
	old := managerInstallation(t, "test.overwrite", true)
	old.IgnoreHostVersion = true
	prior := managerInstallation(t, old.PluginID, false)
	prior.Version = "0.9.0"
	prior.Manifest.Version = prior.Version
	old.Previous = &prior.VersionSnapshot
	require.NoError(t, store.Save(t.Context(), old))
	require.NoError(t, manager.Sync(t.Context(), old.PluginID))
	target := managerInstallation(t, old.PluginID, true)
	target.ManifestSHA256 = "replacement"
	for platform, artifact := range target.Artifacts {
		artifact.SHA256 = "replacement"
		target.Artifacts[platform] = artifact
	}
	service.uploads = versionUploads{snapshot: target.VersionSnapshot}
	processes.failBinary = "replacement"
	_, err := service.Update(t.Context(), old.PluginID, UpdateInput{UploadToken: "reviewed"}, 7)
	require.ErrorIs(t, err, ErrPluginAlreadyInstalled)
	_, err = service.Update(t.Context(), old.PluginID, UpdateInput{UploadToken: "reviewed", Force: true}, 7)
	require.ErrorIs(t, err, ErrUpdateFailed)
	restored, err := store.Get(t.Context(), old.PluginID)
	require.NoError(t, err)
	require.Equal(t, old.VersionSnapshot, restored.VersionSnapshot)
	require.Equal(t, old.Previous, restored.Previous)
	require.Equal(t, StateRunning, manager.Status(old.PluginID).State)
}

// TestNewInstallVersionOverrideChecksBothBounds admits a first installation outside each gateway version boundary.
// Without the option installation fails, and enabling the saved installation later retains the explicitly reviewed version exception.
func TestNewInstallVersionOverrideChecksBothBounds(t *testing.T) {
	for _, bounds := range []struct {
		minimum string // inclusive gateway version requirement
		maximum string // exclusive gateway version requirement
	}{{minimum: "99.0.0"}, {minimum: "0.1.0", maximum: "0.5.0"}} {
		manager, service, _, _, _ := managerFixture(t, 1)
		target := managerInstallation(t, "test.bounds", false)
		target.Manifest.MinHostVersion = bounds.minimum
		target.Manifest.MaxHostVersion = bounds.maximum
		service.uploads = versionUploads{snapshot: target.VersionSnapshot}
		input := InstallInput{Source: SourceUpload, UploadToken: "reviewed"}
		_, err := service.Install(t.Context(), input, 1)
		require.ErrorIs(t, err, ErrPluginIncompatible)
		input.IgnoreHostVersion = true
		result, err := service.Install(t.Context(), input, 1)
		require.NoError(t, err)
		require.True(t, result.Installation.IgnoreHostVersion)
		_, err = service.Enable(t.Context(), target.PluginID, 1)
		require.NoError(t, err)
		require.Equal(t, StateRunning, manager.Status(target.PluginID).State)
	}
}
