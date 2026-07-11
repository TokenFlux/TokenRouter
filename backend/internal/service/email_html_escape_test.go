//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildVerifyCodeEmailBody_EscapesSiteName(t *testing.T) {
	svc := &EmailService{}

	t.Run("escapes_script_injection", func(t *testing.T) {
		body := svc.buildVerifyCodeEmailBody("123456", `</h1><script>alert(1)</script><h1>`)

		assert.NotContains(t, body, "<script>")
		assert.Contains(t, body, "&lt;script&gt;")
	})

	t.Run("escapes_html_entities", func(t *testing.T) {
		body := svc.buildVerifyCodeEmailBody("123456", `A&B<C>"D`)

		assert.Contains(t, body, "A&amp;B&lt;C&gt;&#34;D")
	})

	t.Run("keeps_normal_site_name", func(t *testing.T) {
		body := svc.buildVerifyCodeEmailBody("654321", "My Site")

		assert.Contains(t, body, "<h1>My Site</h1>")
		assert.Contains(t, body, "654321")
	})
}

func TestBuildPasswordResetEmailBody_EscapesHTMLValues(t *testing.T) {
	svc := &EmailService{}

	t.Run("escapes_html_tags_in_site_name", func(t *testing.T) {
		body := svc.buildPasswordResetEmailBody("https://example.com/reset?token=abc", `</h1><img src=x onerror=alert(1)>`)

		assert.NotContains(t, body, "<img src=x")
		assert.Contains(t, body, "&lt;img")
	})

	t.Run("escapes_html_entities", func(t *testing.T) {
		body := svc.buildPasswordResetEmailBody("https://example.com/reset", `A&B<C>`)

		assert.Contains(t, body, "A&amp;B&lt;C&gt;")
	})

	t.Run("keeps_normal_site_name_and_url", func(t *testing.T) {
		resetURL := "https://example.com/reset?token=xyz"
		body := svc.buildPasswordResetEmailBody(resetURL, "TokenRouter")

		assert.Contains(t, body, "<h1>TokenRouter</h1>")
		assert.Contains(t, body, resetURL)
	})

	t.Run("escapes_ampersand_in_reset_url", func(t *testing.T) {
		resetURL := "https://example.com/reset?a=1&b=2"
		body := svc.buildPasswordResetEmailBody(resetURL, "Site")

		assert.NotContains(t, body, `href="https://example.com/reset?a=1&b=2"`)
		assert.Contains(t, body, `href="https://example.com/reset?a=1&amp;b=2"`)
	})
}
