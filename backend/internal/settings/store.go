package settings

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// store.go delegates settings persistence and coordinates local update
// notifications.
// Setting values remain strings until their owning modules interpret them. The
// store delegates persistence and lets those modules decide defaults, validation,
// and runtime cache changes; successful writes themselves do not invoke subscribers.
// The same store also exposes a coordinator for combined updates spanning several
// modules and a version value for public settings responses.
//
// Notification takes a snapshot of registrations while holding the store mutex,
// then executes selected callbacks outside it. Unsubscribe can remove pending
// registrations during notification, while a callback selected before removal can
// finish afterward. This coordination applies within one process and one
// notification's registration order; concurrent notifications can execute callbacks
// at the same time.

var (
	// ErrSettingNotFound identifies an absent saved setting so callers can choose
	// their module's default without depending on storage-specific absence errors.
	ErrSettingNotFound = apperror.NotFound("SETTING_NOT_FOUND", "setting not found")

	// NonConfigurationKeyPrefixes lists per-delivery and per-recipient operational
	// key prefixes stored in the settings table outside administrator configuration.
	NonConfigurationKeyPrefixes = []string{
		"notification_email_delivery:",
		"notification_email_locale:",
		"notification_email_preference:",
	}
)

// Setting holds one stored key/value record and its persistence timestamp.
type Setting struct {
	ID        int64     // stored setting row identifier
	Key       string    // owning module setting key
	Value     string    // raw stored setting text
	UpdatedAt time.Time // latest saved update timestamp
}

// Repository reads and writes settings. Implementations must commit batch writes atomically.
type Repository interface {
	// Get reads one saved record by key. An absent record returns
	// ErrSettingNotFound; other read failures remain errors for the caller's module
	// to handle before interpreting the stored value.
	Get(context.Context, string) (*Setting, error)
	// GetValue reads the string saved for one key and reports read failures.
	// An absent setting returns ErrSettingNotFound in production storage, allowing
	// the owning reader to distinguish absence from a saved empty string.
	GetValue(context.Context, string) (string, error)
	// Set upserts one raw value under its key after caller validation.
	// Completion persists the value; the caller separately refreshes domain state
	// and decides when subscribed readers should be notified.
	Set(context.Context, string, string) error
	// GetMultiple reads the existing records for the supplied keys into a map.
	// Absent keys are omitted, and an empty key list produces an empty map without
	// requiring a storage query.
	GetMultiple(context.Context, []string) (map[string]string, error)
	// SetMultiple upserts the entire prepared batch atomically. Empty input
	// succeeds, and a failed batch cannot expose a subset of its values as the
	// completed update to other readers.
	SetMultiple(context.Context, map[string]string) error
	// GetAll returns the complete set of stored key/value pairs for its caller.
	GetAll(context.Context) (map[string]string, error)
	// Delete removes a saved key and accepts an already absent record.
	// The owning module determines the default after deletion and publishes its
	// runtime change separately from storage completion.
	Delete(context.Context, string) error
}

// ConditionalRepository compares expected values and commits the complete batch atomically.
type ConditionalRepository interface {
	Repository // ordinary settings value operations
	// CompareAndSetMultiple compares each expected string before atomically
	// upserting values. The PostgreSQL writer accepts nil expectations for absent
	// or empty records; conflict returns locale.ErrConflict and prevents the batch.
	CompareAndSetMultiple(context.Context, map[string]string, map[string]*string) error
}

// ConfigurationRepository reads administrator configuration keys without scanning
// unbounded per-delivery or per-recipient operational entries.
type ConfigurationRepository interface {
	// GetConfigurationValues returns a newly allocated map of stored settings
	// excluding NonConfigurationKeyPrefixes.
	GetConfigurationValues(context.Context) (map[string]string, error)
}

// subscription holds a registered callback and its notification admission state.
type subscription struct {
	// active is read before fn is selected outside the store lock. Unsubscribe
	// clears it, while an earlier true observation can still permit one callback
	// for each notification that already selected this registration.
	active atomic.Bool
	id     uint64 // identifier assigned by Store
	fn     func() // registered synchronous callback function
}

// Store delegates value persistence and coordinates local update sessions and
// subscribed readers. Calling modules own value validation, defaults, and runtime
// cache refresh, and call NotifyUpdated after those changes are ready.
// @project-doc docs/interfaces/configuration.md#runtime_settings
type Store struct {
	updates *Updates // combined settings update coordinator
	// repo is retained from New. Persistence methods require this dependency;
	// application assembly owns its lifecycle through shutdown of active update sessions.
	repo Repository
	// mu protects version, nextID, and subscribers. Notifications copy the
	// subscriber slice under mu and release it before callback execution; each
	// subscription's active flag remains atomic for those unlocked checks.
	mu          sync.RWMutex    // version and registration lock
	version     string          // application version display text
	nextID      uint64          // next registration sequence counter
	subscribers []*subscription // callbacks in registration order
}

// IsNonConfigurationKey reports whether key belongs to an operational marker or
// recipient preference prefix excluded from administrator configuration snapshots.
func IsNonConfigurationKey(key string) bool {
	for _, prefix := range NonConfigurationKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// New wraps a repository with setting reads, update coordination, and local
// subscriptions, or returns an existing Store directly. A nil repository supports
// version and subscription operations; persistence calls require an installed
// repository owned by application assembly.
func New(repo Repository) *Store {
	if store, ok := repo.(*Store); ok {
		return store
	}
	return &Store{repo: repo, updates: newUpdates(repo)}
}

// Get delegates lookup of one setting record to the bound repository. Missing
// settings and other errors retain that implementation's classification, and the
// returned record contains its saved value and update time.
func (s *Store) Get(ctx context.Context, key string) (*Setting, error) { return s.repo.Get(ctx, key) }

// GetValue delegates reading one stored string to the bound repository. The
// store supplies no default value; calling modules interpret absent keys and
// read failures according to their own setting rules.
func (s *Store) GetValue(ctx context.Context, key string) (string, error) {
	return s.repo.GetValue(ctx, key)
}

// Set delegates saving one key and value to the bound repository. The caller
// validates that value and refreshes its runtime state before calling NotifyUpdated
// when subscribers should observe the completed change.
func (s *Store) Set(ctx context.Context, key, value string) error { return s.repo.Set(ctx, key, value) }

// GetMultiple delegates a batch read for the supplied keys to the repository.
// The caller's module interprets returned strings and missing entries; the store
// adds no defaults or runtime publication to that result.
func (s *Store) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	return s.repo.GetMultiple(ctx, keys)
}

// SetMultiple delegates the complete batch to the bound repository's atomic
// writer. Calling modules validate and prepare the values first, then refresh
// runtime state and invoke NotifyUpdated when publication should follow persistence.
func (s *Store) SetMultiple(ctx context.Context, values map[string]string) error {
	return s.repo.SetMultiple(ctx, values)
}

// GetAll delegates reading the repository's complete key/value map for its caller.
func (s *Store) GetAll(ctx context.Context) (map[string]string, error) { return s.repo.GetAll(ctx) }

// GetConfigurationValues reads administrator configuration entries while excluding
// operational notification delivery, locale, and preference prefixes. Repositories
// implementing ConfigurationRepository filter in storage; other implementations
// fall back to filtering GetAll results into a fresh map.
func (s *Store) GetConfigurationValues(ctx context.Context) (map[string]string, error) {
	if reader, ok := s.repo.(ConfigurationRepository); ok {
		return reader.GetConfigurationValues(ctx)
	}
	// Filter in memory when a test or custom repository only implements Repository,
	// keeping composite configuration snapshots consistent with SQL prefix exclusion.
	all, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make(map[string]string, len(all))
	for key, value := range all {
		if !IsNonConfigurationKey(key) {
			filtered[key] = value
		}
	}
	return filtered, nil
}

// Delete delegates removal of one setting key to the bound repository. The
// caller decides which default applies after deletion and coordinates any runtime
// refresh and subscriber notification for that change.
func (s *Store) Delete(ctx context.Context, key string) error { return s.repo.Delete(ctx, key) }

// SetVersion publishes the supplied application-version text under the store lock.
func (s *Store) SetVersion(version string) {
	s.mu.Lock()
	s.version = version
	s.mu.Unlock()
}

// Version returns the application-version text currently protected by the store lock.
func (s *Store) Version() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Subscribe registers a synchronous callback and returns an idempotent removal
// function. A nil callback returns a no-op removal function. One NotifyUpdated
// invocation visits its captured registrations in order, while separate notifications
// can invoke the same callback concurrently.
//
// Removal changes the registration's active flag and removes it from later
// snapshots. A notification that has already read that flag as true can still
// invoke the callback after removal returns. Callbacks run outside the store lock
// and may read settings or remove registrations themselves.
func (s *Store) Subscribe(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	sub := &subscription{id: id, fn: fn}
	// Publish the active flag before appending the registration so a later
	// notification snapshot can select a complete callback entry after releasing mu.
	sub.active.Store(true)
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for i, sub := range s.subscribers {
				if sub.id == id {
					// Clear admission before removing the entry so snapshots already copied by
					// notifications can skip it when they have not yet read its active flag.
					sub.active.Store(false)
					s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
					return
				}
			}
		})
	}
}

// NotifyUpdated invokes subscriptions after the calling module has persisted
// values and refreshed its runtime state. It snapshots registrations under the
// store lock, then checks each active flag before invoking its callback in the
// snapshot's order.
//
// Callbacks execute outside that lock so they can read settings and unsubscribe.
// A removal after an active check permits that already selected callback to run;
// separate NotifyUpdated calls can overlap their callback execution. Callback
// panics propagate to the notifying caller, and this method supplies no recovery
// or cross-process delivery mechanism.
func (s *Store) NotifyUpdated() {
	// Copy registrations under the lock, then release it before invoking callbacks
	// so a callback can remove itself or a later registration during this notification.
	s.mu.RLock()
	subs := append([]*subscription(nil), s.subscribers...)
	s.mu.RUnlock()
	for _, sub := range subs {
		if sub.active.Load() {
			sub.fn()
		}
	}
}

// Updates returns the instance update coordinator used by combined settings requests.
func (s *Store) Updates() *Updates { return s.updates }

// StringOrDefault returns a nonempty stored string or the supplied default.
// Whitespace is retained and counts as nonempty, so callers choose whether their
// setting requires trimming or other validation.
func StringOrDefault(values map[string]string, key, fallback string) string {
	if value, ok := values[key]; ok && value != "" {
		return value
	}
	return fallback
}

// IsExplicitFalse reports whether trimmed, case-normalized text names a disabled
// value: false, 0, off, or disabled. Other text, including empty input, returns
// false and leaves the caller to define its enablement default.
func IsExplicitFalse(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return true
	default:
		return false
	}
}
