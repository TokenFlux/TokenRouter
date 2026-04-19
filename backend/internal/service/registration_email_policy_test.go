//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRegistrationEmailSuffixWhitelist(t *testing.T) {
	got, err := NormalizeRegistrationEmailSuffixWhitelist([]string{"example.com", "@EXAMPLE.COM", " @foo.bar "})
	require.NoError(t, err)
	require.Equal(t, []string{"@example.com", "@foo.bar"}, got)
}

func TestNormalizeRegistrationEmailSuffixWhitelist_Invalid(t *testing.T) {
	_, err := NormalizeRegistrationEmailSuffixWhitelist([]string{"@invalid_domain"})
	require.Error(t, err)
}

func TestParseRegistrationEmailSuffixWhitelist(t *testing.T) {
	got := ParseRegistrationEmailSuffixWhitelist(`["example.com","@foo.bar","@invalid_domain"]`)
	require.Equal(t, []string{"@example.com", "@foo.bar"}, got)
}

func TestIsRegistrationEmailSuffixAllowed(t *testing.T) {
	require.True(t, IsRegistrationEmailSuffixAllowed("user@example.com", []string{"@example.com"}))
	require.False(t, IsRegistrationEmailSuffixAllowed("user@sub.example.com", []string{"@example.com"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("user@any.com", []string{}))
}

func TestNormalizeRegistrationEmailAddress(t *testing.T) {
	require.Equal(t, "yourname@example.com", NormalizeRegistrationEmailAddress("Y.o.u.r.N.a.m.e+abc@Example.com"))
	require.Equal(t, "yourname@example.com", NormalizeRegistrationEmailAddress("yourname@example.com"))
	require.Empty(t, NormalizeRegistrationEmailAddress("invalid-email"))
}
