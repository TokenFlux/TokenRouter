package backup

import (
	"context"
	"encoding/json"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Backup setup samples time separately for metadata and dated object placement,
// so crossing midnight can give the filename and storage directory different dates.
// Event-recording fixtures make initial persistence and archive execution order
// observable, including the distinct synchronous and asynchronous responses to
// content-selection or record-write failure.

const (
	// Test setting aliases let the external test package seed legacy and current
	// destinations and inspect the same persisted record key used by the service.
	TestSettingKeyBackupS3Config      = settingKeyBackupS3Config
	TestSettingKeyBackupStorageConfig = settingKeyBackupStorageConfig
	TestSettingKeyBackupRecords       = settingKeyBackupRecords
)

// backupRecordSettings records settings access and attempted record saves for
// ordering assertions.
type backupRecordSettings struct {
	runtimeSettings // delegated settings storage fixture
	// events shares the clock and archive event list. Tests wait for workers
	// before reading it because these fixture appends have no independent synchronization.
	events *[]string
	// records holds the first decoded record from each attempted save, including
	// attempts that the embedded settings fixture later rejects with its injected error.
	records []BackupRecord
}

// backupRecordArchive records archive entry while returning successful empty
// backup output.
type backupRecordArchive struct {
	runtimeArchive // delegated restore-counting archive fixture
	// events is the shared event list appended during Write. Its test owner
	// reads only after synchronous work or the service wait group has completed.
	events *[]string
}

// TestBackupInitialRecord advances the fixture clock across midnight for both
// backup entry paths. It checks initial metadata, the asynchronous response copy,
// and exact setting-read, clock-sample, record-save, and archive event ordering.
func TestBackupInitialRecord(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			var events []string
			repo := &backupRecordSettings{events: &events}
			archive := &backupRecordArchive{events: &events}
			nowCalls := 0
			start := time.Date(2026, 10, 11, 23, 59, 59, 0, time.UTC)
			s := New(repo, Options{Now: func() time.Time {
				events = append(events, "now")
				nowCalls++
				// Advance in seconds so the second sample dates object placement in
				// the next day while initial metadata retains the prior day's timestamp.
				return start.Add(time.Duration(nowCalls-1) * time.Second)
			}}, nil, nil, nil, archive, nil)
			defer s.Stop()
			var record *BackupRecord
			var err error
			if async {
				record, err = s.StartBackup(context.Background(), "manual", 2)
			} else {
				record, err = s.createBackup(context.Background(), "manual", 2)
			}
			require.NoError(t, err)
			// Wait for asynchronous finalization before inspecting the unsynchronized
			// event stream and record snapshots, so every asserted phase has already completed.
			s.wg.Wait()
			require.Len(t, repo.records[0].ID, 8)
			require.Regexp(t, `^[0-9a-f]{8}$`, repo.records[0].ID)
			progress := "dumping"
			if async {
				progress = "pending"
			}
			require.Equal(t, BackupRecord{
				ID: record.ID, Status: "running", BackupType: "postgres",
				FileName: "tr_20261011_235959.sql.gz", StorageType: BackupStorageTypeLocal,
				StorageKey: "2026/10/12/tr_20261011_235959.sql.gz", TriggeredBy: "manual",
				StartedAt: "2026-10-11T23:59:59Z", ExpiresAt: "2026-10-13T23:59:59Z", Progress: progress,
			}, repo.records[0])
			wantEvents := []string{settingKeyBackupStorageConfig, settingKeyBackupS3Config, "now", "now", settingKeyBackupContentConfig}
			if async {
				wantEvents = append(wantEvents, "save:pending")
				require.Equal(t, repo.records[0], *record)
			}
			wantEvents = append(wantEvents, "save:dumping", "archive", "now", "save:")
			require.Equal(t, wantEvents, events)
		})
	}
}

// TestBackupInitialRecordFailure injects invalid content JSON or failed settings
// writes in both entry paths. It checks asynchronous rejection before archive work,
// synchronous failure metadata for invalid content, and continued synchronous
// archiving after record-write failure.
func TestBackupInitialRecordFailure(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, corrupt := range []bool{false, true} {
			var events []string
			repo := &backupRecordSettings{events: &events}
			if corrupt {
				repo.values = map[string]string{settingKeyBackupContentConfig: "invalid JSON"}
			} else {
				repo.fail = true
			}
			archive := &backupRecordArchive{events: &events}
			s := New(repo, Options{Now: func() time.Time {
				return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
			}}, nil, nil, nil, archive, nil)
			var record *BackupRecord
			var err error
			if async {
				record, err = s.StartBackup(context.Background(), "manual", 0)
			} else {
				record, err = s.createBackup(context.Background(), "manual", 0)
			}
			// Finish any launched worker before observing attempted writes or changing
			// fixture lifetime, keeping this result comparison separate from shutdown cancellation effects.
			s.wg.Wait()
			s.Stop()
			if async {
				require.Error(t, err)
				require.Nil(t, record)
				require.NotContains(t, events, "archive")
			} else {
				require.Empty(t, record.ExpiresAt)
				if corrupt {
					require.ErrorIs(t, err, ErrBackupContentConfigCorrupt)
					require.Equal(t, "failed", record.Status)
					require.NotContains(t, events, "archive")
				} else {
					require.NoError(t, err)
					require.Equal(t, "completed", record.Status)
					require.Contains(t, events, "archive")
				}
			}
		}
	}
}

// GetValue logs non-record setting reads before delegating to runtimeSettings.
// Record-list reads are omitted from the event stream so the tests can compare
// setup configuration ordering separately from persistence's internal load operations.
func (r *backupRecordSettings) GetValue(ctx context.Context, key string) (string, error) {
	if key != settingKeyBackupRecords {
		*r.events = append(*r.events, key)
	}
	return r.runtimeSettings.GetValue(ctx, key)
}

// Set records the first decoded record and its progress before delegating the
// write to runtimeSettings. Callers supply nonempty record-list JSON; snapshots
// represent attempted writes even when the delegated fixture returns its
// injected persistence error.
func (r *backupRecordSettings) Set(ctx context.Context, key, value string) error {
	var records []BackupRecord
	if err := json.Unmarshal([]byte(value), &records); err != nil {
		return err
	}
	// Capture before the delegated write can fail, so the fixture distinguishes
	// an attempted initial record from a record successfully stored by runtimeSettings.
	r.records = append(r.records, records[0])
	*r.events = append(*r.events, "save:"+records[0].Progress)
	return r.runtimeSettings.Set(ctx, key, value)
}

// Write appends an archive-entry event and returns zero bytes with success.
// The shared event list is read after operation completion; this fixture ignores
// archive inputs and performs no object transfer or record callback.
func (a *backupRecordArchive) Write(_ context.Context, _ *BackupRecord, _ BackupObjectStore, _ *BackupS3Config, _ BackupDumpOptions, _ func(context.Context, *BackupRecord) error, _ func(time.Duration) (context.Context, context.CancelFunc)) (int64, error) {
	*a.events = append(*a.events, "archive")
	return 0, nil
}

// TestRestoreMustRegister makes the initial restoration-status write fail for
// a completed local record. It requires StartRestore to return an error and confirms,
// after service stop, that the archive executor never received restore work.
func TestRestoreMustRegister(t *testing.T) {
	repo := &runtimeSettings{}
	archive := &runtimeArchive{}
	s := runtimeBackup(repo, archive)
	data, err := json.Marshal([]BackupRecord{{ID: "fixture", Status: "completed", StorageType: "local", StorageKey: "fixture.gz"}})
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupRecords, string(data)))
	repo.fail = true
	_, err = s.StartRestore(context.Background(), "fixture")
	require.Error(t, err)
	// Drain before asserting the restore count, so a delayed worker cannot
	// escape the observation after the initial status registration was rejected.
	s.Stop()
	require.Zero(t, archive.calls.Load())
}

// AccessSaveRecord exposes record upsert for external fixture setup and assertions.
// It delegates record-list locking and error handling to saveRecord; callers provide
// a nonnil record and keep its fields stable while the save runs.
func AccessSaveRecord(s *BackupService, c context.Context, r *BackupRecord) error {
	return s.saveRecord(c, r)
}

// AccessLoadRecords exposes locked record-list loading to external tests. Missing
// or failed reads return no records, while malformed JSON returns the service's
// corruption error. Callers await worker completion before using these decoded
// snapshots for final-outcome assertions.
func AccessLoadRecords(s *BackupService, c context.Context) ([]BackupRecord, error) {
	return s.loadRecords(c)
}

// AccessLoadS3 exposes the legacy configuration reader, including its decrypted
// secret, for external assertions. Missing settings return nil; decryption failure
// retains stored text. External fixtures use this reader to inspect credential
// compatibility separately from public getter redaction.
func AccessLoadS3(s *BackupService, c context.Context) (*BackupS3Config, error) {
	return s.loadS3Config(c)
}

// AccessRecover runs stale-record recovery synchronously for external fixtures.
// The service background context controls its reads and recovery saves, while
// object cleanup uses the separate compensation budget. Callers inspect
// persisted outcomes after this recovery pass returns.
func AccessRecover(s *BackupService) { s.recoverStaleRecords() }

// AccessWait waits for the service's currently tracked operations before external
// assertions. Callers finish operation admission and worker registration first, so
// fixture reads follow completed work rather than racing a future wait-group addition.
func AccessWait(s *BackupService) { s.wg.Wait() }

// AccessBackingUp sets the local backup reservation under its mutex for conflict
// fixtures. Callers use a fresh service and account for this injected flag remaining
// set because no archive worker was launched to clear it.
func AccessBackingUp(s *BackupService) { s.opMu.Lock(); s.backingUp = true; s.opMu.Unlock() }

// AccessArchive returns the shared executor injected into the fixture service.
// External tests customize its archive part size before launching work; later
// operation calls use that same executor, so mutation is completed before workers read it.
func AccessArchive(s *BackupService) ArchiveExecutor { return s.archive }

// TestPluginBusinessTablesRemainInEveryBackup disables every optional data group and checks its exclusion patterns.
// Plugin installation intent, retained KV, and ledger replay evidence must remain included under the default backup policy.
func TestPluginBusinessTablesRemainInEveryBackup(t *testing.T) {
	service := &BackupService{}
	excluded := service.buildExcludedTableData(&BackupContentConfig{})
	for _, table := range []string{"public.plugin_indexes", "public.plugin_installations", "public.plugin_kv", "public.plugin_ledger"} {
		for _, pattern := range excluded {
			matches, err := path.Match(pattern, table)
			require.NoError(t, err)
			require.False(t, matches, "%s excludes %s", pattern, table)
		}
	}
}
