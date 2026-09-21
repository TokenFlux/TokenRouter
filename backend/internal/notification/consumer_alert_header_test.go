//go:build unit

package notification

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeEmailHeader_CRLF(t *testing.T) {
	require.Equal(t, "Subject injected", SanitizeEmailHeader("Subject\r\n injected"))
}

func TestSanitizeEmailHeader_OnlyCR(t *testing.T) {
	require.Equal(t, "foobar", SanitizeEmailHeader("foo\rbar"))
}

func TestSanitizeEmailHeader_OnlyLF(t *testing.T) {
	require.Equal(t, "foobar", SanitizeEmailHeader("foo\nbar"))
}

func TestSanitizeEmailHeader_Clean(t *testing.T) {
	require.Equal(t, "Sub2API", SanitizeEmailHeader("Sub2API"))
}

func TestSanitizeEmailHeader_Empty(t *testing.T) {
	require.Equal(t, "", SanitizeEmailHeader(""))
}

func TestSanitizeEmailHeader_MultipleNewlines(t *testing.T) {
	require.Equal(t, "abc", SanitizeEmailHeader("a\r\nb\r\nc"))
}
