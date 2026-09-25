// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// NotifyEmailEntry represents a notification email with enable/disable and verification state.
// All emails are user-managed; maximum 3 entries per user.
type NotifyEmailEntry struct {
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
	Verified bool   `json:"verified"`
}

// NotifyEmailEntriesFromIdentity converts service entries to DTO entries.
func NotifyEmailEntriesFromIdentity(entries []identity.NotifyEmailEntry) []NotifyEmailEntry {
	if entries == nil {
		return nil
	}
	result := make([]NotifyEmailEntry, len(entries))
	for i, e := range entries {
		result[i] = NotifyEmailEntry{
			Email:    e.Email,
			Disabled: e.Disabled,
			Verified: e.Verified,
		}
	}
	return result
}

// NotifyEmailEntriesToIdentity converts DTO entries to service entries.
func NotifyEmailEntriesToIdentity(entries []NotifyEmailEntry) []identity.NotifyEmailEntry {
	if entries == nil {
		return nil
	}
	result := make([]identity.NotifyEmailEntry, len(entries))
	for i, e := range entries {
		result[i] = identity.NotifyEmailEntry{
			Email:    e.Email,
			Disabled: e.Disabled,
			Verified: e.Verified,
		}
	}
	return result
}
