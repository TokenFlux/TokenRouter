package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExpandConfigString covers required values, defaults, escaping, and invalid syntax.
// Substitution scans only source text, preserves leading zeros, and never includes
// resolved contents in errors when a later required reference is missing.
func TestExpandConfigString(t *testing.T) {
	values := map[string]string{"EMPTY": "", "PASSWORD": "0123", "NESTED": "${MISSING}", "PRIVATE": "private-value"}
	lookup := func(name string) (string, bool) { v, ok := values[name]; return v, ok }
	cases := []struct {
		raw, want string
		fail      bool
	}{
		{"${PASSWORD}", "0123", false},
		{"${EMPTY}", "", false},
		{"${EMPTY:-fallback}", "fallback", false},
		{"${UNSET:-fallback}", "fallback", false},
		{"${PASSWORD:-${MISSING}}", "0123", false},
		{"${UNSET:-${MISSING}}", "${MISSING}", false},
		{"$${PASSWORD}", "${PASSWORD}", false},
		{"prefix/${PASSWORD}/${NESTED}/$x", "prefix/0123/${MISSING}/$x", false},
		{"${PRIVATE}${MISSING}", "", true},
		{"${BAD-NAME}", "", true},
		{"${PASSWORD", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, _, err := expandConfigString("database.password", tc.raw, lookup)
			if tc.fail {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-value")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestInterpolateEnvReferencesCopiesDocument protects on-disk reference spelling.
// Loading changes only the returned graph, keeps resulting scalars tagged as
// strings, and leaves mapping keys literal even when they resemble references.
func TestInterpolateEnvReferencesCopiesDocument(t *testing.T) {
	doc, _, err := parseConfigDocument([]byte("config_version: 2\ndatabase:\n  password: ${PASSWORD}\n"), "config.yaml")
	require.NoError(t, err)
	resolved, err := interpolateEnvReferences(doc, func(string) (string, bool) { return "0123", true })
	require.NoError(t, err)
	require.Equal(t, "${PASSWORD}", configPath(doc.Content[0], "database.password").Value)
	require.Equal(t, "0123", configPath(resolved.Content[0], "database.password").Value)
	require.Equal(t, "!!str", configPath(resolved.Content[0], "database.password").Tag)
}
