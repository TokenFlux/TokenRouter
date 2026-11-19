package provider

import "maps"

// credentials_redact.go merges redacted credential edits while inheriting
// omitted secrets and removes temporary login material.
// Administrator credential objects omit secrets before reaching the UI, so
// submitting one back requires a distinction between an omitted secret and a
// supplied empty value. Secret and ephemeral-login classifications come from
// field_policy.go, keeping redaction and storage sanitization aligned with
// scheduler metadata rules.
//
// MergePreservingSensitiveCreds builds a new top-level map from incoming fields
// and inherits only omitted sensitive entries from existing. Its nested values
// remain shared. SanitizeStoredCredentials instead mutates its supplied map in
// place, removing password, SSO, and cookie keys after a credential exchange so
// those temporary values cannot reappear through a later omitted-secret merge.

// IsSensitiveCredentialKey reports membership in the startup-built sensitive
// credential key set.
func IsSensitiveCredentialKey(key string) bool {
	_, ok := sensitiveCredentialKeySet[key]
	return ok
}

// MergePreservingSensitiveCreds builds a new top-level credential object from
// incoming and inherits omitted sensitive values from existing. A supplied
// secret key wins even when empty or nil.
//
// Nonsensitive fields omitted from incoming disappear, which lets a redacted
// form replace its ordinary configuration fields. Both input maps remain
// unchanged, but their nested mutable values are shared with the returned map.
// Callers that need separate ownership copy those supported values with
// CloneValues before later mutation.
func MergePreservingSensitiveCreds(existing, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(incoming)+len(SensitiveCredentialKeys))
	maps.Copy(out, incoming)
	for _, key := range SensitiveCredentialKeys {
		if _, hasIncoming := incoming[key]; hasIncoming {
			continue
		}
		if existingVal, ok := existing[key]; ok {
			out[key] = existingVal
		}
	}
	return out
}

// SanitizeStoredCredentials removes temporary password, SSO, and cookie keys in
// place and returns the same map. Nil remains nil, and platform is ignored so
// bulk changes that omit platform still sanitize credentials.
func SanitizeStoredCredentials(platform string, creds map[string]any) map[string]any {
	if creds == nil {
		return nil
	}
	_ = platform
	for _, key := range ephemeralLoginCredentialKeys {
		delete(creds, key)
	}
	return creds
}
