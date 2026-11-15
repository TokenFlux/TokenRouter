package backup_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/backup"
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	pg "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// A small compressed dump stays in one remote object, while a tiny configured
// part budget exposes split metadata, per-segment digests, and cleanup of planned
// objects after upload failure. Tests run the archive provider's compression and
// local-file operations, then compare captured bytes or recorded object keys.
// Remote storage and database execution are in-memory fixtures, so these checks
// establish archive orchestration and input preparation rather than live S3 or
// PostgreSQL outcomes.
//
// Background tests retain a closed dump gate while checking initial responses or
// a second admission, then release it and wait before inspecting final metadata.
// Fixture locks protect settings and object bookkeeping, while dumper observations
// and direct fixture configuration are accessed after work or before launch.
// SQL mocks exercise injected advisory-lock acquisition and release calls; the
// production ownership-token maintenance implementation is covered in its own tests.

var (
	// ErrSettingNotFound marks absent entries returned by the fixture's Get method;
	// GetValue uses empty successful text for missing settings to match service readers.
	ErrSettingNotFound = errors.New("setting not found")
	// databaseHeavyMaintenanceLockID identifies the advisory key checked by SQL
	// expectations when injected maintenance callbacks attempt database acquisition or release.
	databaseHeavyMaintenanceLockID = pg.HashAdvisoryLockID("maintenance:database-heavy")
)

// mockSettingRepo stores fixture settings and counts reads under its mutex.
type mockSettingRepo struct {
	// mu protects data, getValueErr, and getValueCalls during fixture methods.
	// Direct control configuration happens before work starts; returned string-valued
	// maps are copies that callers can change independently of this fixture.
	mu          sync.Mutex        // fixture setting state lock
	data        map[string]string // stored setting text values
	getValueErr error             // optional injected read error
	// getValueCalls counts GetValue attempts before either injected failure or
	// map lookup. Tests inspect this count after work or while holding mu.
	getValueCalls int // attempted setting read count
}

// plainEncryptor models secret preparation and decoding with a reversible text prefix.
type plainEncryptor struct{}

// mockDumper supplies export bytes and captures successfully consumed restore input.
type mockDumper struct {
	// dumpData is shared with bytes.NewReader when export begins. Tests keep its
	// backing bytes stable until archive work has finished consuming the returned reader.
	dumpData []byte
	dumpErr  error // optional export initiation error
	// restored retains the last fully read restore input. An injected error or
	// reader failure returns before replacement; assertions read after operation completion.
	restored []byte
	restErr  error // optional restore initiation error
	// opts retains the latest successful Dump call's options, including its shared
	// exclusion slice. Tests keep supplied options stable and inspect them after execution.
	opts backup.BackupDumpOptions
}

// blockingDumper gates export initiation and observes waiting-operation cancellation.
type blockingDumper struct {
	// blockCh releases waiting Dump calls when closed; cancellation can also end
	// waiting. Tests retain the gate through admission assertions before releasing archive execution.
	blockCh chan struct{}
	// data supplies reader bytes after gate release. Its backing storage stays
	// stable through consumption, and cancellation before release can return without a reader.
	data    []byte
	restErr error // optional restore initiation error
	// opts is assigned after gate admission, retaining the supplied exclusion
	// slice. Test assertions inspect it after the admitted archive operation has completed.
	opts backup.BackupDumpOptions
}

// mockObjectStore stores archive bytes and records deterministic transfer failures.
type mockObjectStore struct {
	// mu protects objects, uploadFileCalls, deletedKeys, failUploadFileAt, and
	// failDeleteKeys during methods. Direct configuration happens before work;
	// assertions use mu or wait until workers finish reading or changing this state.
	//
	// objects retains byte slices shared with Download readers. Tests seed or
	// alter those bytes before use and keep a downloaded slice stable during consumption.
	objects map[string][]byte // archive object byte map
	mu      sync.Mutex        // object fixture bookkeeping lock
	// failUploadFileAt selects a positive file-upload attempt number to reject
	// before consuming body. Nonpositive values let every attempted upload reach Upload.
	failUploadFileAt int      // selected failed file-upload attempt
	uploadFileCalls  int      // file upload attempt count
	deletedKeys      []string // attempted object deletion keys
	// failDeleteKeys intercepts deletion for any present key, returning its value
	// before removal. Tests populate nonnil errors to observe failed objects remaining;
	// a present nil value would also leave the object while returning success.
	failDeleteKeys map[string]error // configured object deletion errors
}

// testArchive delegates execution with fixture-controlled exports and archive
// segmentation.
type testArchive struct {
	// dumper is shared across the short-lived provider executors constructed here.
	// Tests configure it before launch and await completion before reading its captured output.
	dumper backup.DBDumper
	// partSize overrides each provider executor's segment budget when positive;
	// nonpositive values choose its default. Test mutation finishes before execution reads this field.
	partSize int64
}

// Setting holds one copied key/value entry returned by the settings fixture.
type Setting struct {
	// Key and Value copy a fixture map entry into Get's result. Later map
	// replacements do not alter these strings, and mutating this record does not update that map.
	Key, Value string // Key: fixture setting name; Value: stored setting text
}

// newMockSettingRepo allocates initialized empty setting storage for external tests.
func newMockSettingRepo() *mockSettingRepo {
	return &mockSettingRepo{data: make(map[string]string)}
}

// Get returns a copied Setting under the fixture mutex or ErrSettingNotFound.
// It ignores context and leaves GetValue's failure control and counter untouched;
// changing the returned record does not modify stored strings.
func (m *mockSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: v}, nil
}

// GetValue counts the attempt and returns fixture text under the mutex.
// An injected read error wins over lookup; missing keys return empty successful
// text, and the supplied context does not cancel this fixture operation.
func (m *mockSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getValueCalls++
	if m.getValueErr != nil {
		return "", m.getValueErr
	}
	v, ok := m.data[key]
	if !ok {
		return "", nil
	}
	return v, nil
}

// Set replaces fixture text under the settings mutex and returns success.
// It ignores context and accepts any supplied string, allowing tests to seed
// malformed JSON whose validation is performed by the service reader.
func (m *mockSettingRepo) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

// GetMultiple copies present requested entries into a new string-valued map
// under the fixture lock. Missing keys are omitted and context is ignored;
// callers can modify the returned map independently of stored settings.
func (m *mockSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string)
	for _, k := range keys {
		if v, ok := m.data[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

// SetMultiple copies all supplied entries into existing fixture storage under
// the mutex and returns success. Context is ignored; callers keep the source
// map stable while it is copied, and unrelated stored entries remain.
func (m *mockSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Copy the complete batch while reads share this mutex, so GetMultiple and
	// GetAll cannot observe an intermediate subset of the supplied setting replacements.
	maps.Copy(m.data, settings)
	return nil
}

// GetAll copies the complete string-valued settings map while holding the
// fixture mutex. Context is ignored; callers own the returned map and can replace
// or delete its entries without modifying fixture storage.
func (m *mockSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string, len(m.data))
	maps.Copy(result, m.data)
	return result, nil
}

// Delete removes a fixture key under the settings mutex and returns success.
// Missing keys are accepted and context is ignored; later Get and GetValue use
// their different absent-entry results for that key.
func (m *mockSettingRepo) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// Encrypt models secret persistence by returning plaintext with an ENC: prefix.
// The result remains reversible test text; service assertions use it to observe
// preparation and public redaction, and no cryptographic operation is performed.
func (e *plainEncryptor) Encrypt(plaintext string) (string, error) {
	return "ENC:" + plaintext, nil
}

// Decrypt strips the ENC: fixture prefix when present and returns the remainder.
// Unprefixed text is returned with an error, allowing service readers to exercise
// their compatibility branch that retains stored values after failed decoding.
func (e *plainEncryptor) Decrypt(ciphertext string) (string, error) {
	if after, ok := strings.CutPrefix(ciphertext, "ENC:"); ok {
		return after, nil
	}
	return ciphertext, fmt.Errorf("not encrypted")
}

// Dump returns an injected error or a reader sharing configured export bytes.
// Successful calls retain opts, including its exclusion slice; callers close the
// reader and keep fixture data stable until the archive finishes consuming it.
func (m *mockDumper) Dump(_ context.Context, opts backup.BackupDumpOptions) (io.ReadCloser, error) {
	if m.dumpErr != nil {
		return nil, m.dumpErr
	}
	m.opts = opts
	return io.NopCloser(bytes.NewReader(m.dumpData)), nil
}

// Restore reads the supplied stream into captured bytes unless an error is
// injected first. Reader errors propagate before captured state is replaced;
// context is ignored. Tests inspect the last successful input after operation
// completion.
func (m *mockDumper) Restore(_ context.Context, data io.Reader) error {
	if m.restErr != nil {
		return m.restErr
	}
	d, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.restored = d
	return nil
}

// Dump waits for its release gate or context cancellation before returning
// configured export bytes. Cancellation selected during waiting returns the context
// error before options are captured; gate admission stores opts and returns a
// reader sharing data that the test keeps stable.
//
// The test owner closes blockCh after checking admission or initial response, so
// successful export admission waits for that release. Cancellation can instead
// complete waiting with an error. When both events are ready, select can choose
// either; this fixture supplies no priority.
// Worker assertions wait for operation completion before reading captured options.
func (d *blockingDumper) Dump(ctx context.Context, opts backup.BackupDumpOptions) (io.ReadCloser, error) {
	// Keep export waiting until the test releases its gate or shutdown cancels
	// the context, allowing admission assertions before a background archive can finish.
	select {
	case <-d.blockCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	d.opts = opts
	return io.NopCloser(bytes.NewReader(d.data)), nil
}

// Restore returns an injected error or consumes the input with success.
// This fixture ignores context, its export gate, and input-read errors; callers
// use mockDumper for captured restore bytes or propagated reader failures.
func (d *blockingDumper) Restore(_ context.Context, data io.Reader) error {
	if d.restErr != nil {
		return d.restErr
	}
	// This lifecycle fixture consumes input but suppresses reader errors; tests
	// needing restore-byte or input-error observations select mockDumper's separate captured-input behavior.
	_, _ = io.ReadAll(data)
	return nil
}

// newMockObjectStore allocates object and deletion-failure maps for archive fixtures.
func newMockObjectStore() *mockObjectStore {
	return &mockObjectStore{
		objects:        make(map[string][]byte),
		failDeleteKeys: make(map[string]error),
	}
}

// Upload reads the complete source before publishing its bytes under the
// object mutex and reports their length. Read errors leave the map unchanged;
// context is ignored, and the caller retains responsibility for closing body.
func (m *mockObjectStore) Upload(_ context.Context, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	// Publish only the fully read buffer after input success, so consumers never
	// see a partially read replacement and a source error leaves existing bytes intact.
	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()
	return int64(len(data)), nil
}

// UploadFile assigns an attempt number and rejects the configured positive
// number before reading body, otherwise delegating to Upload. It records attempted
// calls even when failure is injected, letting cleanup tests select a planned
// segment that was registered before an unsuccessful transfer.
//
// Attempt counting and failure selection are read together under mu, then the
// lock is released before body consumption or delegated object publication.
// Overlapping calls can therefore finish in a different order from their assigned
// numbers. Tests set the failure control before work and keep each source reader
// available until its transfer call returns.
func (m *mockObjectStore) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	// Assign the attempt number and snapshot its failure control together, then
	// release the lock before reading body so another transfer can claim its own number.
	m.mu.Lock()
	m.uploadFileCalls++
	call := m.uploadFileCalls
	failAt := m.failUploadFileAt
	m.mu.Unlock()
	if failAt > 0 && call == failAt {
		return 0, fmt.Errorf("injected upload failure at call %d", call)
	}
	return m.Upload(ctx, key, body, contentType)
}

// Download returns a reader sharing an existing object's stored byte slice,
// or a missing-object error. It ignores context; callers close the reader and
// keep the referenced bytes stable, even if a later map replacement uses another slice.
func (m *mockObjectStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	data, ok := m.objects[key]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete logs the attempted key under mu and returns a present failDeleteKeys
// value before removal, including nil values. Other keys are removed with success,
// accepting missing objects; context is ignored, and tests inject nonnil errors for failures.
func (m *mockObjectStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	// Record the attempt before consulting the injected failure; cleanup assertions
	// can distinguish a failed deletion that reached this fixture from an unattempted key.
	m.deletedKeys = append(m.deletedKeys, key)
	if err, ok := m.failDeleteKeys[key]; ok {
		m.mu.Unlock()
		return err
	}
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

// PresignURL constructs a deterministic fixture URL using the supplied key.
// It ignores expiry, context, and object existence, so assertions check response
// selection and key association rather than remote signing or access validity.
func (m *mockObjectStore) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://presigned.example.com/" + key, nil
}

// HeadBucket returns success for every fixture destination access probe.
// Configured connection tests can complete after credential presence validation.
// No remote API is called, so these assertions cannot establish permission to
// transfer archives.
func (m *mockObjectStore) HeadBucket(_ context.Context) error {
	return nil
}

// newTestBackupService constructs an inactive fixture service with persistent
// secret-key availability enabled. It delegates temporary local storage and fake
// remote/dumper wiring to the configurable constructor. Callers start and stop
// lifecycle work when their scenario needs it.
func newTestBackupService(t *testing.T, repo *mockSettingRepo, dumper backup.DBDumper, store *mockObjectStore) *backup.BackupService {
	return newTestBackupServiceWithEncryptionKey(t, repo, dumper, store, true)
}

// newTestBackupServiceWithEncryptionKey constructs an inactive service using a
// temporary local directory, prefix encryptor, shared fake remote store, and real
// archive executor wrapper. The flag controls new-secret persistence; nil maintenance
// uses the service's successful no-op claim until a test rebinds it.
func newTestBackupServiceWithEncryptionKey(t *testing.T, repo *mockSettingRepo, dumper backup.DBDumper, store *mockObjectStore, encryptionKeyConfigured bool) *backup.BackupService {
	t.Helper()
	path := t.TempDir()
	factory := func(context.Context, *backup.BackupS3Config) (backup.BackupObjectStore, error) { return store, nil }
	svc := backup.New(repo, backup.Options{LocalPath: path, EncryptionKeyConfigured: encryptionKeyConfigured, Now: time.Now}, &plainEncryptor{}, factory, bp.NewLocalBackupStore(path), &testArchive{dumper: dumper}, nil)

	return svc
}

// seedS3Config stores a complete legacy remote fixture with ENC-prefixed secret.
// The initialized repository accepts the string-valued settings without validation;
// callers seed before work, and service reads decode the prefix for connection use.
func seedS3Config(t *testing.T, repo *mockSettingRepo) {
	t.Helper()
	cfg := backup.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret123",
		Prefix:          "backups",
	}
	// These literal settings contain supported primitive fields, so encoding is
	// treated as fixture setup; the following Set assertion checks storing the prepared JSON text.
	data, _ := json.Marshal(cfg)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, string(data)))
}

// seedStorageS3Config writes matching unified and legacy remote fixtures with
// an ENC-prefixed secret. Callers seed before operation launch; the shared configuration
// supports current destination selection and legacy credential lookup with
// the default upload mode.
func seedStorageS3Config(t *testing.T, repo *mockSettingRepo) {
	t.Helper()
	s3Cfg := backup.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret123",
		Prefix:          "backups",
	}
	cfg := backup.BackupStorageConfig{
		Type: backup.BackupStorageTypeS3,
		S3:   s3Cfg,
	}
	// Encode supported primitive fixture fields, then store unified settings before
	// their matching legacy copy so subsequent readers can exercise either configuration source.
	data, _ := json.Marshal(cfg)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupStorageConfig, string(data)))
	s3Data, _ := json.Marshal(s3Cfg)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, string(s3Data)))
}

// TestBackupService_S3ConfigEncryption saves a secret using the fixture prefix
// encryptor. It checks ENC-prefixed stored text, public legacy getter redaction,
// and the decoded secret exposed by the internal configuration reader.
func TestBackupService_S3ConfigEncryption(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// Persist through the fixture encryptor before reading both public and stored
	// representations, so the ENC assertion observes preparation rather than real cryptographic security.
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "my-secret",
		Prefix:          "backups",
	})
	require.NoError(t, err)

	// Read the successful fixture write with its failure control unset, then
	// decode and compare preparation separately from the public getter's redaction assertions.
	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupS3Config)
	var stored backup.BackupS3Config
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, "ENC:my-secret", stored.SecretAccessKey)

	cfg, err := svc.GetS3Config(context.Background())
	require.NoError(t, err)
	require.Empty(t, cfg.SecretAccessKey)
	require.Equal(t, "my-bucket", cfg.Bucket)

	// Inspect the internal reader's decoded secret separately from public redaction;
	// the remote factory does not capture credentials in this fixture or verify actual authentication.
	internal, err := backup.AccessLoadS3(svc, context.Background())
	require.NoError(t, err)
	require.Equal(t, "my-secret", internal.SecretAccessKey)
}

// TestBackupService_S3ConfigKeepExistingSecret updates the access ID while
// omitting a previously saved secret. It requires the internal reader to retain
// the original decoded secret and return the newly supplied access ID.
func TestBackupService_S3ConfigKeepExistingSecret(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "original-secret",
	})
	require.NoError(t, err)

	// Omit the secret during the second update to exercise retained stored text,
	// then decode it internally to check credential reuse alongside the changed access identifier.
	_, err = svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:      "my-bucket",
		AccessKeyID: "AKID-NEW",
	})
	require.NoError(t, err)

	internal, err := backup.AccessLoadS3(svc, context.Background())
	require.NoError(t, err)
	require.Equal(t, "original-secret", internal.SecretAccessKey)
	require.Equal(t, "AKID-NEW", internal.AccessKeyID)
}

// TestBackupService_UpdateS3ConfigRejectsEphemeralKey supplies a new secret with
// persistent key availability disabled. It requires the key-configuration error
// and checks that the legacy setting remains empty after the rejected update.
func TestBackupService_UpdateS3ConfigRejectsEphemeralKey(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false)

	// Disable deployment key stability before submitting a fresh credential; the
	// key-configuration error must precede any legacy settings write checked by the final read.
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "my-secret",
	})
	require.ErrorIs(t, err, backup.ErrSecretEncryptionKeyNotConfigured)

	// Missing fixture keys return empty successful text; this final read checks
	// that rejected secret preparation did not leave saved legacy configuration behind.
	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupS3Config)
	require.Empty(t, raw)
}

// TestBackupService_UpdateStorageConfigRejectsEphemeralKey supplies new remote
// credentials through unified settings with key availability disabled. It requires
// the key-configuration error and an empty unified setting after rejection.
func TestBackupService_UpdateStorageConfigRejectsEphemeralKey(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false)

	// Exercise the unified update's new-secret branch with unstable-key availability;
	// the final empty-setting assertion detects a write occurring before credential preparation rejected it.
	_, err := svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{
		Type: backup.BackupStorageTypeS3,
		S3: backup.BackupS3Config{
			Bucket:          "my-bucket",
			AccessKeyID:     "AKID",
			SecretAccessKey: "my-secret",
		},
	})
	require.ErrorIs(t, err, backup.ErrSecretEncryptionKeyNotConfigured)

	// The fixture's unset read-error control permits inspecting the empty unified
	// value after rejection, independently of the earlier key-configuration error assertion.
	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupStorageConfig)
	require.Empty(t, raw)
}

// TestBackupService_EphemeralKeyAllowsExistingSecretReuse seeds a prefixed legacy
// secret while persistent key availability is disabled. A secret-omitting update
// must succeed and leave the retained ENC-prefixed credential in stored text.
func TestBackupService_EphemeralKeyAllowsExistingSecretReuse(t *testing.T) {
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	svc := newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false)

	// Retained credential text is already prepared by the seed helper; omission
	// should allow its reuse even when this service cannot safely encrypt a new supplied secret.
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:      "my-bucket",
		AccessKeyID: "AKID-NEW",
	})
	require.NoError(t, err)

	// Read through the fixture's successful lookup to check retained encoded text;
	// the preceding method-success assertion alone would not detect changed credential representation.
	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupS3Config)
	require.Contains(t, raw, "ENC:secret123")
}

// TestBackupService_EncryptionKeyConfigured constructs fixtures with enabled and
// disabled persistent key flags. It requires each service to report its supplied
// availability flag, independently of the shared prefix encryptor used by both.
func TestBackupService_EncryptionKeyConfigured(t *testing.T) {
	repo := newMockSettingRepo()
	require.True(t, newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore()).EncryptionKeyConfigured())
	require.False(t, newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false).EncryptionKeyConfigured())
}

// TestBackupService_SaveRecordConcurrency launches twenty record saves with
// distinct identifiers. After joining the writers, it requires a decodable list
// with twenty entries, checking the count after concurrent record-list updates.
func TestBackupService_SaveRecordConcurrency(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	var wg sync.WaitGroup
	n := 20 // Keep the batch below the service metadata retention cap.
	// Join every writer before counting persisted entries. The final list length
	// exposes lost saves even though individual save errors are discarded by these worker callbacks.
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			record := &backup.BackupRecord{
				ID:        fmt.Sprintf("rec-%d", idx),
				Status:    "completed",
				StartedAt: time.Now().Format(time.RFC3339),
			}
			_ = backup.AccessSaveRecord(svc, context.Background(), record)
		}(i)
	}
	wg.Wait()

	records, err := backup.AccessLoadRecords(svc, context.Background())
	require.NoError(t, err)
	require.Len(t, records, n)
}

// TestBackupService_LoadRecords_Empty reads an initialized fixture with no
// record-list setting. It requires no error and a nil slice, distinguishing
// this accepted empty state from a decoded allocated record collection.
func TestBackupService_LoadRecords_Empty(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	records, err := backup.AccessLoadRecords(svc, context.Background())
	require.NoError(t, err)
	require.Nil(t, records) // Missing records are a valid empty state.
}

// TestBackupService_LoadRecords_Corrupted seeds non-JSON text as the record list.
// It requires the internal loader to return an error and nil records for that
// stored input rather than accepting it as an empty list.
func TestBackupService_LoadRecords_Corrupted(t *testing.T) {
	repo := newMockSettingRepo()
	// Seed invalid JSON through the fixture's always-successful text writer, so
	// the following internal load exercises record decoding rather than setup write validation.
	_ = repo.Set(context.Background(), backup.TestSettingKeyBackupRecords, "not valid json{{{")
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	records, err := backup.AccessLoadRecords(svc, context.Background())
	require.Error(t, err) // Invalid stored JSON produces an error result.
	require.Nil(t, records)
}

// TestBackupService_CreateBackup_Streaming creates a small remote archive using
// default upload settings. It checks completed status, positive compressed size,
// a retained legacy key, and exactly one object in the fixture store.
func TestBackupService_CreateBackup_Streaming(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "-- PostgreSQL dump\nCREATE TABLE test (id int);\n"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Greater(t, record.SizeBytes, int64(0))
	require.NotEmpty(t, record.S3Key)

	store.mu.Lock()
	require.Len(t, store.objects, 1)
	store.mu.Unlock()
}

// TestBackupService_CreateBackup_SplitsSpooledPutArchive sets a thirty-two-byte
// segment budget for remote spooled upload. It checks ordered keys, segment sizes
// and digests, exact decompressed content, cleared single-object keys, and
// total compressed size.
func TestBackupService_CreateBackup_SplitsSpooledPutArchive(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	dumpContent := entropyBackupFixture(2048)
	dumper := &mockDumper{dumpData: dumpContent}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	// A tiny segment budget exposes multipart archive metadata on modest fixture data.
	archive.partSize = 32

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Greater(t, len(record.Parts), 1)
	require.Empty(t, record.StorageKey)
	require.Empty(t, record.S3Key)

	var compressed bytes.Buffer
	store.mu.Lock()
	for i, part := range record.Parts {
		require.Equal(t, i+1, part.Index)
		require.Equal(t, part.StorageKey, part.S3Key)
		data, ok := store.objects[part.StorageKey]
		require.True(t, ok)
		require.LessOrEqual(t, int64(len(data)), int64(32))
		require.Equal(t, int64(len(data)), part.SizeBytes)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), part.SHA256)
		_, err := compressed.Write(data)
		require.NoError(t, err)
	}
	store.mu.Unlock()

	gzReader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	require.NoError(t, err)
	decompressed, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	require.NoError(t, gzReader.Close())
	require.Equal(t, dumpContent, decompressed)
	require.Equal(t, int64(compressed.Len()), record.SizeBytes)
}

// TestBackupService_CreateBackup_MultipartModeKeepsSingleObject selects multipart
// mode with a tiny archive segment budget. It requires one stored object, retained
// single-object keys, and no split-part metadata after successful backup creation.
func TestBackupService_CreateBackup_MultipartModeKeepsSingleObject(t *testing.T) {
	repo := newMockSettingRepo()
	s3Cfg := backup.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret123",
		Prefix:          "backups",
		UploadMode:      backup.BackupS3UploadModeMultipart,
	}
	storageData, err := json.Marshal(backup.BackupStorageConfig{Type: backup.BackupStorageTypeS3, S3: s3Cfg})
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupStorageConfig, string(storageData)))
	s3Data, err := json.Marshal(s3Cfg)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, string(s3Data)))

	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: entropyBackupFixture(2048)}, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	// Retain the tiny archive budget while testing multipart mode's single-object selection.
	archive.partSize = 32
	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Empty(t, record.Parts)
	require.NotEmpty(t, record.StorageKey)
	require.NotEmpty(t, record.S3Key)
	store.mu.Lock()
	require.Len(t, store.objects, 1)
	store.mu.Unlock()
}

// TestBackupService_CreateBackup_PartUploadFailureCleansPlannedObjects rejects the
// second file-upload attempt. It requires failed status with two planned segments,
// and checks deletion attempts and absent objects for both registered keys.
func TestBackupService_CreateBackup_PartUploadFailureCleansPlannedObjects(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	// Reject the second attempt after its plan is registered, leaving the first
	// successful object and the failed candidate key available to the enclosing cleanup path.
	store.failUploadFileAt = 2
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: entropyBackupFixture(2048)}, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	// Force several small segments so the selected second upload is reached.
	archive.partSize = 32

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.Error(t, err)
	require.Equal(t, "failed", record.Status)
	require.Len(t, record.Parts, 2)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, part := range record.Parts {
		require.Contains(t, store.deletedKeys, part.StorageKey)
		require.NotContains(t, store.objects, part.StorageKey)
	}
}

// entropyBackupFixture returns deterministic repeated byte data of the requested
// size for compression scenarios. Callers supply a nonnegative size; the periodic
// pattern leaves enough compressed bytes for these tests' small segment budgets.
func entropyBackupFixture(size int) []byte {
	data := make([]byte, size)
	// Use a periodic byte pattern for reproducible gzip segmentation assertions.
	for i := range data {
		data[i] = byte((i*31 + 17) % 251)
	}
	return data
}

// TestBackupService_CreateBackup_DumpFailure injects an export initiation error
// from the dumper fixture. It requires backup failure and a failed record whose
// diagnostic contains the pg_dump source of that error.
func TestBackupService_CreateBackup_DumpFailure(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &mockDumper{dumpErr: fmt.Errorf("pg_dump failed")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.Error(t, err)
	require.Equal(t, "failed", record.Status)
	require.Contains(t, record.ErrorMsg, "pg_dump")
}

// TestBackupService_CreateBackup_DefaultLocalStorage creates an archive with no
// remote configuration. It checks completed local metadata and reads the application
// download body through gzip to require the original fixture content.
func TestBackupService_CreateBackup_DefaultLocalStorage(t *testing.T) {
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("local data")}
	svc := newTestBackupService(t, repo, dumper, newMockObjectStore())

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Equal(t, backup.BackupStorageTypeLocal, record.StorageType)
	require.NotEmpty(t, record.StorageKey)
	require.Empty(t, record.S3Key)

	body, _, err := svc.OpenBackupDownload(context.Background(), record.ID)
	require.NoError(t, err)
	// Attempt body closure after the content assertion, releasing the local file
	// even on test failure while suppressing cleanup errors separate from gzip and byte checks.
	defer func() { _ = body.Close() }()

	gzReader, err := gzip.NewReader(body)
	require.NoError(t, err)
	restored, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	require.Equal(t, "local data", string(restored))
}

// TestBackupService_UpdateStorageConfig_LocalPreservesS3Config switches a configured
// remote destination to local. It checks remembered bucket and access ID in the
// public response, redacted secret, and retained ENC-prefixed secret in unified storage.
func TestBackupService_UpdateStorageConfig_LocalPreservesS3Config(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	_, err := svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{
		Type: backup.BackupStorageTypeS3,
		S3: backup.BackupS3Config{
			Bucket:          "remote-bucket",
			AccessKeyID:     "AKID",
			SecretAccessKey: "remote-secret",
			Prefix:          "backups",
		},
	})
	require.NoError(t, err)

	// Change destination kind after saving credentials, then compare public fields
	// and stored secret text to detect accidental credential removal or plaintext replacement during the switch.
	_, err = svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{Type: backup.BackupStorageTypeLocal})
	require.NoError(t, err)

	cfg, err := svc.GetStorageConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeLocal, cfg.Type)
	require.Equal(t, "remote-bucket", cfg.S3.Bucket)
	require.Equal(t, "AKID", cfg.S3.AccessKeyID)
	require.Empty(t, cfg.S3.SecretAccessKey)

	raw, err := repo.GetValue(context.Background(), backup.TestSettingKeyBackupStorageConfig)
	require.NoError(t, err)
	var stored backup.BackupStorageConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, backup.BackupStorageTypeLocal, stored.Type)
	require.Equal(t, "ENC:remote-secret", stored.S3.SecretAccessKey)
}

// TestBackupService_GetStorageConfig_MergesLegacyS3WhenUnifiedConfigEmpty seeds
// local unified settings with an empty remote object and complete legacy credentials.
// It checks retained local selection, inherited bucket and access ID, and
// public secret redaction.
func TestBackupService_GetStorageConfig_MergesLegacyS3WhenUnifiedConfigEmpty(t *testing.T) {
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	data, err := json.Marshal(backup.BackupStorageConfig{Type: backup.BackupStorageTypeLocal})
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupStorageConfig, string(data)))

	// Keep local selection with an empty embedded S3 object so the getter must
	// inherit remembered legacy credentials while clearing its secret for the public response.
	cfg, err := svc.GetStorageConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeLocal, cfg.Type)
	require.Equal(t, "test-bucket", cfg.S3.Bucket)
	require.Equal(t, "AKID", cfg.S3.AccessKeyID)
	require.Empty(t, cfg.S3.SecretAccessKey)
}

// TestBackupService_ContentConfigDefaultsExcludeLargeHistory starts with no
// content policy. It checks all optional flags disabled, thirty-five returned
// exclusion patterns, and representative usage, Ops, and runtime exclusions
// delivered to the dumper.
func TestBackupService_ContentConfigDefaultsExcludeLargeHistory(t *testing.T) {
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("data")}
	svc := newTestBackupService(t, repo, dumper, newMockObjectStore())

	cfg, err := svc.GetContentConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.IncludeUsageRecords)
	require.False(t, cfg.IncludeOpsLogs)
	require.False(t, cfg.IncludeAuditLogs)
	require.False(t, cfg.IncludeRuntimeData)
	require.Contains(t, cfg.ExcludedTableData, "public.ops_error_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_user_activity")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_analytics_hourly")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_analytics_daily")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_analytics_aggregation_state")
	require.Contains(t, cfg.ExcludedTableData, "public.pending_auth_sessions")
	require.Contains(t, cfg.ExcludedTableData, "public.identity_adoption_decisions")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_export_tasks")
	require.Len(t, cfg.ExcludedTableData, 38)
	require.Contains(t, cfg.ExcludedTableData, "public.billing_system_usage_entries")
	require.Contains(t, cfg.ExcludedTableData, "public.provider_balance_alert_states")
	require.Contains(t, cfg.ExcludedTableData, "public.request_records")

	_, err = svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Contains(t, dumper.opts.ExcludeTableData, "public.ops_error_logs")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.usage_billing_dedup")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.identity_adoption_decisions")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.usage_export_tasks")
}

// TestBackupService_ContentConfigCanIncludeSelectedData enables usage and audit
// history while leaving Ops and runtime disabled. It checks representative returned
// exclusions and the resulting dump options for selected and omitted groups.
func TestBackupService_ContentConfigCanIncludeSelectedData(t *testing.T) {
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("data")}
	svc := newTestBackupService(t, repo, dumper, newMockObjectStore())

	cfg, err := svc.UpdateContentConfig(context.Background(), backup.BackupContentConfig{
		IncludeUsageRecords: true,
		IncludeOpsLogs:      false,
		IncludeAuditLogs:    true,
		IncludeRuntimeData:  false,
	})
	require.NoError(t, err)
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_logs")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_user_activity")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_analytics_hourly")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_analytics_daily")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_analytics_aggregation_state")
	require.NotContains(t, cfg.ExcludedTableData, "public.payment_audit_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.ops_error_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.scheduler_outbox")

	_, err = svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.NotContains(t, dumper.opts.ExcludeTableData, "public.usage_logs")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.ops_system_logs")
}

// TestBackupService_CreateBackup_ConcurrentBlocked injects the local backingUp
// flag before calling synchronous backup. It requires ErrBackupInProgress for that
// reservation; this fixture scenario does not launch a competing dump worker.
func TestBackupService_CreateBackup_ConcurrentBlocked(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	// Inject the service reservation directly to reach same-kind rejection; this
	// test controls the local flag rather than waiting for another dump operation to become active.
	dumper := &mockDumper{dumpData: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	backup.AccessBackingUp(svc)

	_, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.ErrorIs(t, err, backup.ErrBackupInProgress)
}

// TestBackupService_RestoreBackup_Streaming creates a small remote archive and
// restores it through the provider. It requires successful restoration and exact
// uncompressed SQL fixture bytes captured by the in-memory dumper.
func TestBackupService_RestoreBackup_Streaming(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "-- PostgreSQL dump\nCREATE TABLE test (id int);\n"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	err = svc.RestoreBackup(context.Background(), record.ID)
	require.NoError(t, err)

	// Compare captured database input after gzip restoration, so successful method
	// return alone cannot hide changed SQL bytes in this in-memory export/restore round trip.
	require.Equal(t, dumpContent, string(dumper.restored))
}

// TestBackupService_RestoreBackup_SplitPartsWithLegacyS3Keys seeds compressed
// segments using legacy part keys and no storage type. It checks remote inference
// and successful restoration of the exact original uncompressed fixture bytes.
func TestBackupService_RestoreBackup_SplitPartsWithLegacyS3Keys(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	dumpContent := entropyBackupFixture(1024)
	dumper := &mockDumper{}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	compressed := gzipBackupBytes(t, dumpContent)
	parts := splitBackupBytes(compressed, 17)
	recordParts := make([]backup.BackupPart, 0, len(parts))
	// Seed only legacy per-part keys and omit top-level destination fields below;
	// remote inference must still let the archive provider locate this complete compressed stream.
	for i, data := range parts {
		key := fmt.Sprintf("backups/legacy-split/payload.part-%06d", i+1)
		store.objects[key] = data
		recordParts = append(recordParts, backup.BackupPart{
			Index:     i + 1,
			S3Key:     key,
			SizeBytes: int64(len(data)),
			SHA256:    fmt.Sprintf("%x", sha256.Sum256(data)),
		})
	}
	record := &backup.BackupRecord{
		ID:        "legacy-split",
		Status:    "completed",
		Parts:     recordParts,
		SizeBytes: int64(len(compressed)),
	}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	require.Equal(t, backup.BackupStorageTypeS3, backup.RecordEffectiveStorageType(record))
	require.NoError(t, svc.RestoreBackup(context.Background(), record.ID))
	require.Equal(t, dumpContent, dumper.restored)
}

// TestBackupService_RestoreBackup_InvalidSplitPartDoesNotTouchDatabase seeds a
// size or digest mismatch for one stored segment. It requires the corresponding
// mismatch diagnostic and an empty captured restore input in each fixture scenario.
func TestBackupService_RestoreBackup_InvalidSplitPartDoesNotTouchDatabase(t *testing.T) {
	tests := []struct {
		name      string // metadata mismatch scenario name
		sizeBytes int64  // recorded compressed object bytes
		sha256    string // recorded optional digest text
		wantErr   string // expected mismatch diagnostic fragment
	}{
		{name: "size", sizeBytes: 4, wantErr: "size mismatch"},
		{name: "checksum", sizeBytes: 3, sha256: "invalid", wantErr: "checksum mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockSettingRepo()
			seedStorageS3Config(t, repo)
			dumper := &mockDumper{}
			store := newMockObjectStore()
			svc := newTestBackupService(t, repo, dumper, store)
			key := "backups/invalid-split/payload.part-000001"
			store.objects[key] = []byte("abc")
			record := &backup.BackupRecord{
				ID:     "invalid-split-" + tt.name,
				Status: "completed",
				Parts: []backup.BackupPart{{
					Index:      1,
					StorageKey: key,
					SizeBytes:  tt.sizeBytes,
					SHA256:     tt.sha256,
				}},
			}
			require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

			err := svc.RestoreBackup(context.Background(), record.ID)
			require.ErrorContains(t, err, tt.wantErr)
			require.Empty(t, dumper.restored)
		})
	}
}

// gzipBackupBytes compresses supplied fixture content and completes the trailer
// before returning bytes. It fails the test on write or close error; callers
// use the finished gzip stream as input for legacy segment restoration.
func gzipBackupBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	_, err := writer.Write(content)
	require.NoError(t, err)
	// Finish the gzip trailer before segmenting or storing these bytes, so the
	// fixture represents a complete compressed stream for the restore consumer's checksum validation.
	require.NoError(t, writer.Close())
	return out.Bytes()
}

// splitBackupBytes copies data into independently allocated consecutive segments
// of at most partSize bytes and returns nil for empty input. Callers pass a
// positive budget. Zero loops without progress on nonempty data, and negative
// budgets panic when slicing; returned segment bytes do not share the source.
func splitBackupBytes(data []byte, partSize int) [][]byte {
	var parts [][]byte
	for len(data) > 0 {
		size := min(len(data), partSize)
		// Copy each segment before advancing the source view, so stored part bytes
		// can be manipulated independently of the original compressed fixture and neighboring segments.
		parts = append(parts, append([]byte(nil), data[:size]...))
		data = data[size:]
	}
	return parts
}

// TestBackupService_RestoreBackup_NotCompleted seeds a failed archive record and
// requests synchronous restoration. It requires an error for that identifier,
// checking rejection when the fixture supplies no completed archive for restoration.
func TestBackupService_RestoreBackup_NotCompleted(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// Save supported fixture metadata through the always-successful repository,
	// then request restoration of the failed state so the service's completion check is exercised.
	_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:     "fail-1",
		Status: "failed",
	})

	err := svc.RestoreBackup(context.Background(), "fail-1")
	require.Error(t, err)
}

// TestBackupService_RestoreMaintenanceBusy injects an advisory-lock query that
// reports contention for each restore entry path. It requires the shared maintenance
// busy error and verifies the expected query and hashed lock identifier.
func TestBackupService_RestoreMaintenanceBusy(t *testing.T) {
	tests := []struct {
		name string                            // restore entry path label
		run  func(*backup.BackupService) error // selected restore invocation function
	}{
		{
			name: "synchronous restore",
			run: func(svc *backup.BackupService) error {
				return svc.RestoreBackup(context.Background(), "backup-id")
			},
		},
		{
			name: "asynchronous restore",
			run: func(svc *backup.BackupService) error {
				_, err := svc.StartRestore(context.Background(), "backup-id")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			// Close this SQL fixture after its query assertions. Suppress fixture-close
			// errors so they do not replace the acquisition outcome checked in this subtest.
			defer func() { _ = db.Close() }()

			mock.ExpectQuery("SELECT pg_try_advisory_lock").
				WithArgs(databaseHeavyMaintenanceLockID).
				WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

			svc := newTestBackupService(t, newMockSettingRepo(), &mockDumper{}, newMockObjectStore())
			setTestMaintenance(svc, db)
			require.ErrorIs(t, tt.run(svc), backup.ErrDatabaseMaintenanceBusy)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestBackupService_DeleteBackup creates one remote archive and then deletes it.
// It checks that the object map becomes empty and metadata lookup returns
// ErrBackupNotFound after the successful deletion operation.
func TestBackupService_DeleteBackup(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "data"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	store.mu.Lock()
	require.Len(t, store.objects, 1)
	store.mu.Unlock()

	err = svc.DeleteBackup(context.Background(), record.ID)
	require.NoError(t, err)

	store.mu.Lock()
	require.Len(t, store.objects, 0)
	store.mu.Unlock()

	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.ErrorIs(t, err, backup.ErrBackupNotFound)
}

// TestBackupService_DeleteBackup_RunningKeepsPartObjects seeds a running record
// with two stored segments. It requires the backup-in-progress error, no deletion
// attempts, both objects retained, and metadata still available after rejection.
func TestBackupService_DeleteBackup_RunningKeepsPartObjects(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	parts := []backup.BackupPart{
		{Index: 1, StorageKey: "backups/running/payload.part-000001", SizeBytes: 3},
		{Index: 2, StorageKey: "backups/running/payload.part-000002", SizeBytes: 3},
	}
	for _, part := range parts {
		store.objects[part.StorageKey] = []byte("abc")
	}
	record := &backup.BackupRecord{ID: "running-parts", Status: "running", StorageType: backup.BackupStorageTypeS3, Parts: parts}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	err := svc.DeleteBackup(context.Background(), record.ID)
	require.ErrorIs(t, err, backup.ErrBackupInProgress)
	store.mu.Lock()
	require.Empty(t, store.deletedKeys)
	for _, part := range parts {
		require.Contains(t, store.objects, part.StorageKey)
	}
	store.mu.Unlock()
	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
}

// TestBackupService_DeleteBackup_PartFailureKeepsRecord makes one of two segment
// deletions fail. It checks the returned diagnostic, attempts for both keys,
// and retention of the failed object's bytes and the archive metadata.
func TestBackupService_DeleteBackup_PartFailureKeepsRecord(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	parts := []backup.BackupPart{
		{Index: 1, StorageKey: "backups/delete/payload.part-000001", SizeBytes: 3},
		{Index: 2, StorageKey: "backups/delete/payload.part-000002", SizeBytes: 3},
	}
	for _, part := range parts {
		store.objects[part.StorageKey] = []byte("abc")
	}
	store.failDeleteKeys[parts[1].StorageKey] = errors.New("delete failed")
	record := &backup.BackupRecord{ID: "delete-parts", Status: "completed", StorageType: backup.BackupStorageTypeS3, Parts: parts}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	err := svc.DeleteBackup(context.Background(), record.ID)
	require.ErrorContains(t, err, "delete failed")
	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	store.mu.Lock()
	require.Contains(t, store.objects, parts[1].StorageKey)
	for _, part := range parts {
		require.Contains(t, store.deletedKeys, part.StorageKey)
	}
	store.mu.Unlock()
}

// TestBackupService_DeleteLocalBackupAfterSwitchToS3 creates a local record before
// seeding remote destination settings. Deletion must succeed, metadata lookup must
// report the record missing, and the remote fixture object map must remain empty.
func TestBackupService_DeleteLocalBackupAfterSwitchToS3(t *testing.T) {
	repo := newMockSettingRepo()

	dumper := &mockDumper{dumpData: []byte("local data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeLocal, record.StorageType)

	seedStorageS3Config(t, repo)
	err = svc.DeleteBackup(context.Background(), record.ID)
	require.NoError(t, err)

	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.ErrorIs(t, err, backup.ErrBackupNotFound)
	store.mu.Lock()
	require.Len(t, store.objects, 0)
	store.mu.Unlock()
}

// TestBackupService_RestoreS3BackupAfterSwitchToLocal creates a remote archive,
// then selects local storage through the settings API. It requires successful
// restoration of that earlier remote record and its exact original content.
func TestBackupService_RestoreS3BackupAfterSwitchToLocal(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "remote data"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeS3, record.StorageType)

	_, err = svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{Type: backup.BackupStorageTypeLocal})
	require.NoError(t, err)

	err = svc.RestoreBackup(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, dumpContent, string(dumper.restored))
}

// TestBackupService_GetDownloadURL requests download metadata for a completed
// single-object remote archive. It requires the deterministic remote URL prefix
// and an empty part list, checking the single-object response shape.
func TestBackupService_GetDownloadURL(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &mockDumper{dumpData: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	download, err := svc.GetBackupDownloadURL(context.Background(), record.ID)
	require.NoError(t, err)
	require.Contains(t, download.URL, "https://presigned.example.com/")
	require.Empty(t, download.Parts)
}

// TestBackupService_GetDownloadURL_SplitParts seeds out-of-order segments with
// current and legacy key fields. It checks sorted indices, the first segment's
// recorded size and exact fixture URL, and absence of a single-object URL.
func TestBackupService_GetDownloadURL_SplitParts(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	record := &backup.BackupRecord{
		ID:          "split-download",
		Status:      "completed",
		StorageType: backup.BackupStorageTypeS3,
		Parts: []backup.BackupPart{
			{Index: 2, S3Key: "backups/split/payload.part-000002", SizeBytes: 7},
			{Index: 1, StorageKey: "backups/split/payload.part-000001", SizeBytes: 5},
		},
	}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	download, err := svc.GetBackupDownloadURL(context.Background(), record.ID)
	require.NoError(t, err)
	require.Empty(t, download.URL)
	require.Len(t, download.Parts, 2)
	require.Equal(t, 1, download.Parts[0].Index)
	require.Equal(t, int64(5), download.Parts[0].SizeBytes)
	require.Equal(t, "https://presigned.example.com/backups/split/payload.part-000001", download.Parts[0].URL)
	require.Equal(t, 2, download.Parts[1].Index)
}

// TestBackupService_ListBackups_Sorted saves three consistently formatted start
// timestamps spaced an hour apart. It requires all three records, with the newest
// identifier first and the oldest identifier last in the returned list.
func TestBackupService_ListBackups_Sorted(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	now := time.Now()
	// Space supported fixture records by whole hours under one timestamp format;
	// final identifier ordering checks the loaded sequence after these setup saves discard their errors.
	for i := range 3 {
		_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
			ID:        fmt.Sprintf("rec-%d", i),
			Status:    "completed",
			StartedAt: now.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		})
	}

	records, err := svc.ListBackups(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 3)

	require.Equal(t, "rec-2", records[0].ID)
	require.Equal(t, "rec-0", records[2].ID)
}

// TestBackupService_TestS3Connection supplies all three required credential
// fields to the always-successful object-store fixture. It requires the service
// connection test to return success after passing its configuration presence checks.
func TestBackupService_TestS3Connection(t *testing.T) {
	repo := newMockSettingRepo()
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)

	err := svc.TestS3Connection(context.Background(), backup.BackupS3Config{
		Bucket:          "test",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
	})
	require.NoError(t, err)
}

// TestBackupService_TestS3Connection_Incomplete supplies a bucket while omitting
// both credential fields. It requires the connection test to return an error
// containing the incomplete-configuration diagnostic before probe success.
func TestBackupService_TestS3Connection_Incomplete(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	err := svc.TestS3Connection(context.Background(), backup.BackupS3Config{
		Bucket: "test",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "incomplete")
}

// TestBackupService_Schedule_CronValidation uses an inactive service and supplies
// enabled settings with empty or invalid cron expressions. It requires errors
// for both inputs before an initialized scheduler is available to the service.
func TestBackupService_Schedule_CronValidation(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())
	// Keep the service inactive while testing malformed expressions, so these
	// errors exercise supplied policy validation before startup can provide an installed cron scheduler.

	_, err := svc.UpdateSchedule(context.Background(), backup.BackupScheduleConfig{
		Enabled:  true,
		CronExpr: "",
	})
	require.Error(t, err)

	_, err = svc.UpdateSchedule(context.Background(), backup.BackupScheduleConfig{
		Enabled:  true,
		CronExpr: "invalid",
	})
	require.Error(t, err)
}

// TestBackupService_LoadS3Config_Corrupted stores malformed legacy configuration
// text and calls the internal reader. It requires an error and nil configuration,
// checking that invalid present JSON is distinguishable from a usable decoded object.
func TestBackupService_LoadS3Config_Corrupted(t *testing.T) {
	repo := newMockSettingRepo()
	// Store invalid JSON as accepted fixture text, isolating the internal legacy
	// reader's decode failure from the repository's unrelated key storage and success behavior.
	_ = repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, "not json!!!!")
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	cfg, err := backup.AccessLoadS3(svc, context.Background())
	require.Error(t, err)
	require.Nil(t, cfg)
}

// TestStartBackup_ReturnsImmediately keeps export behind a closed fixture gate.
// It checks that StartBackup returns an identified running record before release,
// then requires completed positive-size metadata after opening the gate and joining work.
func TestStartBackup_ReturnsImmediately(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &blockingDumper{blockCh: make(chan struct{}), data: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "running", record.Status)
	require.NotEmpty(t, record.ID)

	// Release only after initial response assertions, then join worker completion;
	// this ordering distinguishes asynchronous admission from final archive and metadata success.
	close(dumper.blockCh)
	backup.AccessWait(svc)

	final, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", final.Status)
	require.Greater(t, final.SizeBytes, int64(0))
}

// TestStartBackup_CreatesSplitParts launches a spooled remote archive with a tiny
// segment budget. After worker completion, it requires completed metadata with
// multiple segments and both single-object key fields cleared.
func TestStartBackup_CreatesSplitParts(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: entropyBackupFixture(2048)}, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	// Use a tiny segment budget to expose asynchronous split-plan publication.
	archive.partSize = 32

	record, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	// Wait for finalization before loading metadata, so assertions inspect the
	// completed split plan rather than whichever pending or partial plan was last stored during upload.
	backup.AccessWait(svc)

	final, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", final.Status)
	require.Greater(t, len(final.Parts), 1)
	require.Empty(t, final.StorageKey)
	require.Empty(t, final.S3Key)
}

// TestStartBackup_ConcurrentBlocked reserves an asynchronous backup while its
// export gate remains closed. It requires a second StartBackup call to report
// ErrBackupInProgress, then releases and joins the first operation.
func TestStartBackup_ConcurrentBlocked(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &blockingDumper{blockCh: make(chan struct{}), data: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	_, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	// Keep the first export gate closed through the second admission attempt,
	// preventing normal worker completion from clearing the reservation before its conflict is checked.
	_, err = svc.StartBackup(context.Background(), "manual", 14)
	require.ErrorIs(t, err, backup.ErrBackupInProgress)

	// Return the fixture gate after conflict assertions and join the accepted
	// worker, keeping its background archive work from escaping the test's fixture lifetime.
	close(dumper.blockCh)
	backup.AccessWait(svc)
}

// TestStartBackup_ShuttingDown begins service shutdown before requesting a new
// asynchronous backup. It requires admission to fail with the shutdown diagnostic,
// checking the stopped lifetime's rejection before archive execution can be launched.
func TestStartBackup_ShuttingDown(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: []byte("data")}, newMockObjectStore())

	svc.BeginStopContext(context.Background())

	_, err := svc.StartBackup(context.Background(), "manual", 14)
	require.Error(t, err)
	require.Contains(t, err.Error(), "shutting down")
}

// TestRecoverStaleRecords seeds running backup and restore states on separate
// records and invokes recovery directly. It checks each failed outcome and its
// server-restart diagnostic, including restore failure on a completed archive.
func TestRecoverStaleRecords(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// Seed running backup metadata directly; recovery selects this state regardless
	// of the supplied timestamp's age, so the fixture needs no actual process restart.
	_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:        "stale-1",
		Status:    "running",
		StartedAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})
	// Keep backup Status completed while restoration is running, so recovery must
	// change the independent restore outcome instead of treating the archive itself as an interrupted upload.
	_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:            "stale-2",
		Status:        "completed",
		RestoreStatus: "running",
		StartedAt:     time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})

	backup.AccessRecover(svc)

	// The seeded identifiers remain in this readable fixture after recovery;
	// inspect both outcomes directly so assertions distinguish backup failure from independent restore failure.
	r1, _ := svc.GetBackupRecord(context.Background(), "stale-1")
	require.Equal(t, "failed", r1.Status)
	require.Contains(t, r1.ErrorMsg, "server restart")

	r2, _ := svc.GetBackupRecord(context.Background(), "stale-2")
	require.Equal(t, "failed", r2.RestoreStatus)
	require.Contains(t, r2.RestoreError, "server restart")
}

// TestRecoverStaleRecords_CleansRegisteredParts seeds a running remote record
// with two legacy-key segments. After direct recovery, it requires failed metadata,
// deletion attempts for both keys, and no corresponding objects in the fixture store.
func TestRecoverStaleRecords_CleansRegisteredParts(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	parts := []backup.BackupPart{
		{Index: 1, S3Key: "backups/stale/payload.part-000001", SizeBytes: 3},
		{Index: 2, S3Key: "backups/stale/payload.part-000002", SizeBytes: 3},
	}
	for _, part := range parts {
		store.objects[part.S3Key] = []byte("abc")
	}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:          "stale-parts",
		Status:      "running",
		StorageType: backup.BackupStorageTypeS3,
		Parts:       parts,
		StartedAt:   time.Now().Add(-time.Hour).Format(time.RFC3339),
	}))

	backup.AccessRecover(svc)

	record, err := svc.GetBackupRecord(context.Background(), "stale-parts")
	require.NoError(t, err)
	require.Equal(t, "failed", record.Status)
	store.mu.Lock()
	for _, part := range parts {
		require.Contains(t, store.deletedKeys, part.S3Key)
		require.NotContains(t, store.objects, part.S3Key)
	}
	store.mu.Unlock()
}

// TestGracefulShutdown leaves an asynchronous dump gate closed and requests
// StopContext with a one-second budget. It checks successful service drain and
// persisted failed backup status after cancellation ends the waiting export.
func TestGracefulShutdown(t *testing.T) {
	repo := newMockSettingRepo()
	store := newMockObjectStore()
	seedS3Config(t, repo)
	s := newTestBackupService(t, repo, &blockingDumper{blockCh: make(chan struct{})}, store)
	record, err := s.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	// Bound shutdown while the export gate stays closed, so successful drain
	// must come from service cancellation ending the wait rather than a normal gate release.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, s.StopContext(ctx))
	final, err := s.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", final.Status)
}

// TestStartRestore_Async restores a completed record through an injected
// advisory-lock fixture. It checks the initial running response, completed metadata
// after joining the worker, and expected acquisition and unlock calls for
// the operation.
func TestStartRestore_Async(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "-- PostgreSQL dump\nCREATE TABLE test (id int);\n"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	// Close the mock handle after worker and SQL expectation checks. Fixture-close
	// errors are suppressed independently from the acquisition and release calls observed below.
	defer func() { _ = db.Close() }()
	sqlMock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(databaseHeavyMaintenanceLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	sqlMock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(databaseHeavyMaintenanceLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	setTestMaintenance(svc, db)

	restored, err := svc.StartRestore(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "running", restored.RestoreStatus)

	// Join the restore worker before checking completion and unlock expectations;
	// the worker's deferred maintenance release must run before fixture teardown or final assertions.
	backup.AccessWait(svc)

	final, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", final.RestoreStatus)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// Write delegates compression and upload to a newly constructed provider
// executor using the fixture dumper and part budget. The mutable record and callbacks
// follow provider requirements. Fixture customization finishes before this call.
func (a *testArchive) Write(ctx context.Context, r *backup.BackupRecord, store backup.BackupObjectStore, cfg *backup.BackupS3Config, o backup.BackupDumpOptions, save func(context.Context, *backup.BackupRecord) error, cleanup func(time.Duration) (context.Context, context.CancelFunc)) (int64, error) {
	return bp.NewArchive(a.dumper, a.partSize).Write(ctx, r, store, cfg, o, save, cleanup)
}

// Restore delegates archive validation, decompression, and captured database
// input to a newly constructed provider executor. The shared dumper and configured
// part budget remain stable during work; the provider owns downloaded reader cleanup.
func (a *testArchive) Restore(ctx context.Context, r *backup.BackupRecord, store backup.BackupObjectStore) error {
	return bp.NewArchive(a.dumper, a.partSize).Restore(ctx, r, store)
}

// setTestMaintenance binds an advisory-lock callback before tested operations
// begin. The supplied SQL mock expects the hashed heavy-maintenance key; its release
// uses the acquired connection, allowing assertions of acquisition, contention,
// and unlock ordering in these fixtures.
func setTestMaintenance(s *backup.BackupService, db *sql.DB) {
	s.SetMaintenanceLock(func(ctx context.Context) (func(), bool, error) {
		return pg.TryAcquireDBAdvisoryLockWithError(ctx, db, pg.HashAdvisoryLockID("maintenance:database-heavy"))
	})
}
