//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRegistrationEmailSuffixWhitelist(t *testing.T) {
	got, err := NormalizeRegistrationEmailSuffixWhitelist([]string{"example.com", "@EXAMPLE.COM", " @foo.bar ", "*.EDU.CN"})
	require.NoError(t, err)
	require.Equal(t, []string{"@example.com", "@foo.bar", "*.edu.cn"}, got)
}

func TestNormalizeRegistrationEmailSuffixWhitelist_Invalid(t *testing.T) {
	for _, item := range []string{"@invalid_domain", "*.", "*", "*.@", "*.foo"} {
		t.Run(item, func(t *testing.T) {
			_, err := NormalizeRegistrationEmailSuffixWhitelist([]string{item})
			require.Error(t, err)
		})
	}
}

func TestParseRegistrationEmailSuffixWhitelist(t *testing.T) {
	got := ParseRegistrationEmailSuffixWhitelist(`["example.com","@foo.bar","*.EDU.CN","@invalid_domain","*.foo"]`)
	require.Equal(t, []string{"@example.com", "@foo.bar", "*.edu.cn"}, got)
}

func TestIsRegistrationEmailSuffixAllowed(t *testing.T) {
	require.True(t, IsRegistrationEmailSuffixAllowed("user@example.com", []string{"@example.com"}))
	require.False(t, IsRegistrationEmailSuffixAllowed("user@sub.example.com", []string{"@example.com"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("user@qq.com", []string{"@qq.com"}))
	require.False(t, IsRegistrationEmailSuffixAllowed("user@sub.qq.com", []string{"@qq.com"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("student@cs.edu.cn", []string{"*.edu.cn"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("student@edu.cn", []string{"*.edu.cn"}))
	require.False(t, IsRegistrationEmailSuffixAllowed("student@foo.cn", []string{"*.edu.cn"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("user@a.com", []string{"@a.com", "*.b.cn"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("user@school.b.cn", []string{"@a.com", "*.b.cn"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("user@b.cn", []string{"@a.com", "*.b.cn"}))
	require.False(t, IsRegistrationEmailSuffixAllowed("user@c.cn", []string{"@a.com", "*.b.cn"}))
	require.True(t, IsRegistrationEmailSuffixAllowed("user@any.com", []string{}))
}

func TestIsRegistrationEmailSuffixLimited(t *testing.T) {
	require.False(t, IsRegistrationEmailSuffixLimited("user@custom.example", nil))
	require.False(t, IsRegistrationEmailSuffixLimited("user@example.com", []string{"@example.com"}))
	require.True(t, IsRegistrationEmailSuffixLimited("user@custom.example", []string{"@example.com"}))
}

func TestRegistrationEmailDomainUsesRegistrableDomain(t *testing.T) {
	require.Equal(t, "abc.com", RegistrationEmailDomain("user@abc.com"))
	require.Equal(t, "abc.com", RegistrationEmailDomain("user@sub.abc.com"))
	require.Equal(t, "example.co.uk", RegistrationEmailDomain("user@team.example.co.uk"))
	require.Equal(t, "example.com", RegistrationEmailDomain("user@team.example.com."))
}

func TestNormalizeRegistrationEmailAddress(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{name: "Gmail 点号与标签", email: "Y.o.u.r.N.a.m.e+abc@Gmail.com", want: "yourname@gmail.com"},
		{name: "Googlemail 同族与根点", email: "your.name+promo@GoogleMail.com.", want: "yourname@gmail.com"},
		{name: "非 Gmail 保留点号", email: "First.Last+promo@QQ.com", want: "first.last@qq.com"},
		{name: "非 Gmail 无点号是不同身份", email: "firstlast@qq.com", want: "firstlast@qq.com"},
		{name: "首字符加号不折叠", email: "+alice@gmail.com", want: "+alice@gmail.com"},
		{name: "纯点号本地部分不折叠为空", email: "...@gmail.com", want: "...@gmail.com"},
		{name: "非法邮箱", email: "invalid-email", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeRegistrationEmailAddress(tt.email))
		})
	}
}
