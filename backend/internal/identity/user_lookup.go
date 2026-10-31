package identity

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// user_lookup.go exposes bounded exact account discovery without administrative
// search semantics. Callers choose IDs, username, or email, and receive only
// profile observations suitable for an integration adapter. Password hashes,
// administrative notes, authentication credentials, and API keys are absent from
// the result type, so an exact lookup cannot accidentally inherit fuzzy search's
// wider matching fields or its full account representation.
//
// The existing user service discovers this narrow repository capability without
// expanding every profile repository implementation. Production storage supplies
// the exact reader; missing implementations fail explicitly. Selection validation
// stays in identity core, while PostgreSQL owns case-insensitive email equality,
// deleted-user exclusion, stable ordering, and the bounded joined avatar projection.

// ErrUserLookupInvalid distinguishes unsupported exact-selection input from persistence failures without including submitted identifiers or profile text in the message.
var ErrUserLookupInvalid = apperror.BadRequest("USER_LOOKUP_INVALID", "Choose up to 100 user IDs, one exact username, or one exact email")

// UserLookup selects exactly one bounded account-discovery criterion without fuzzy matching.
type UserLookup struct {
	IDs      []int64 // exact positive account identifiers
	Username string  // exact case-sensitive account username
	Email    string  // exact case-insensitive stored email
}

// UserLookupRecord contains profile observations without account credentials or administrative notes.
type UserLookupRecord struct {
	ID           int64      // local account row identifier
	Email        string     // stored account email address
	Username     string     // stored account display name
	Role         string     // stored global account role
	Status       string     // stored account lifecycle status
	CreatedAt    time.Time  // account record creation timestamp
	LastActiveAt *time.Time // optional latest panel activity timestamp
	SignupSource string     // recorded account registration source
	AvatarURL    string     // stored public avatar location
}

// UserExactLookupReader supplies exact, nondeleted profile records for integration discovery.
type UserExactLookupReader interface {
	// LookupUsersExact returns at most one hundred matching profiles in ID order.
	// The caller supplies a normalized single criterion. Email matching ignores case,
	// and username or email selectors never inspect notes, keys, or other identities.
	LookupUsersExact(context.Context, UserLookup) ([]UserLookupRecord, error)
}

// Normalize returns an independently owned, bounded, single-criterion lookup value.
// Outer text whitespace is trimmed. IDs must be positive, and text bounds follow
// the account schema's username and email sizes without rewriting their stored spelling.
func (q UserLookup) Normalize() (UserLookup, error) {
	q.Username = strings.TrimSpace(q.Username)
	q.Email = strings.TrimSpace(q.Email)
	choices := 0
	if len(q.IDs) > 0 {
		choices++
	}
	if q.Username != "" {
		choices++
	}
	if q.Email != "" {
		choices++
	}
	if choices != 1 || len(q.IDs) > 100 || !utf8.ValidString(q.Username) || !utf8.ValidString(q.Email) || utf8.RuneCountInString(q.Username) > 100 || utf8.RuneCountInString(q.Email) > 255 || strings.ContainsRune(q.Username, 0) || strings.ContainsRune(q.Email, 0) {
		return UserLookup{}, ErrUserLookupInvalid
	}
	for _, id := range q.IDs {
		if id <= 0 {
			return UserLookup{}, ErrUserLookupInvalid
		}
	}
	q.IDs = slices.Clone(q.IDs)
	return q, nil
}

// LookupUsersExact returns bounded profile matches through the dedicated exact repository capability.
// It never delegates to administrative fuzzy search. Unsupported repositories fail
// explicitly, and input validation happens before any storage method is invoked.
func (s *UserService) LookupUsersExact(ctx context.Context, input UserLookup) ([]UserLookupRecord, error) {
	query, err := input.Normalize()
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("exact user lookup is unavailable")
	}
	reader, ok := s.userRepo.(UserExactLookupReader)
	if !ok {
		return nil, errors.New("exact user lookup is unavailable")
	}
	return reader.LookupUsersExact(ctx, query)
}
