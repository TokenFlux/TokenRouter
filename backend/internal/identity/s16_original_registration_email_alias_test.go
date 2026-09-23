//go:build unit

package identity_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEmailForAliasDedup(t *testing.T) {
	cases := []struct {
		name  string
		email string
		want  string
	}{
		{name: "plain", email: "user@example.com", want: "user@example.com"},
		{name: "case and spaces", email: "  User@Example.COM ", want: "user@example.com"},
		{name: "plus alias", email: "user+tag@example.com", want: "user@example.com"},
		{name: "gmail plus alias", email: "someone+bulk@gmail.com", want: "someone@gmail.com"},
		{name: "gmail dots", email: "some.one@gmail.com", want: "someone@gmail.com"},
		{name: "googlemail", email: "user@googlemail.com", want: "user@gmail.com"},
		{name: "root dot", email: "first.last@qq.com.", want: "first.last@qq.com"},
		{name: "leading plus", email: "+alice@gmail.com", want: "+alice@gmail.com"},
		{name: "invalid", email: "not-an-email", want: "not-an-email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, identity.NormalizeEmailForAliasDedup(tc.email))
		})
	}
}

func TestEmailAliasDedupProbes(t *testing.T) {
	require.ElementsMatch(t,
		[]identity.EmailAliasProbe{
			{Local: "someone", Domain: "gmailcom"},
			{Local: "someone", Domain: "googlemailcom"},
		},
		identity.EmailAliasDedupProbes("Some.One+tag@gmail.com"),
	)
	require.Equal(t,
		[]identity.EmailAliasProbe{{Local: "firstlast", Domain: "qqcom"}},
		identity.EmailAliasDedupProbes("first.last+tag@qq.com"),
	)
	require.Nil(t, identity.EmailAliasDedupProbes("not-an-email"))
	require.Nil(t, identity.EmailAliasDedupProbes("...@gmail.com"))
}
