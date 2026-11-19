package httpconfig

// options.go defines browser CORS and CSP configuration and the default
// content security policy.
// Browser integrations need their script, frame, and connection sources allowed
// by the response policy. The default CSP includes authentication, CAPTCHA,
// payment, and exact tf CLI loopback origins, while middleware interprets CORS
// and CSP options during handler construction.

// DefaultCSPPolicy supplies the baseline browser policy, including the nonce
// placeholder replaced per HTML request by SecurityHeaders and frontend serving.
const DefaultCSPPolicy = "default-src 'self'; worker-src 'self' blob:; script-src 'self' __CSP_NONCE__ https://accounts.google.com/gsi/client https://challenges.cloudflare.com https://*.alicdn.com https://static.cloudflareinsights.com https://turing.captcha.qcloud.com https://turing.captcha.gtimg.com https://ca.turing.captcha.qcloud.com https://global.turing.captcha.gtimg.com https://www.tycaptcha.com https://cloudcache.tencentcs.com https://*.stripe.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; style-src 'self' 'unsafe-inline' https://*.captcha.gtimg.com https://accounts.google.com/gsi/style https://fonts.googleapis.com https://*.alicdn.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; img-src 'self' data: blob: https:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' http://127.0.0.1:43110 http://127.0.0.1:43111 http://127.0.0.1:43112 http://127.0.0.1:43113 http://127.0.0.1:43114 http://127.0.0.1:43115 http://127.0.0.1:43116 http://127.0.0.1:43117 http://127.0.0.1:43118 http://127.0.0.1:43119 https://accounts.google.com/gsi/ https://turing.captcha.qcloud.com https://www.tycaptcha.com https://rce.tencentrio.com https:; frame-src https://accounts.google.com/gsi/ https://challenges.cloudflare.com https://turing.captcha.qcloud.com https://ca.turing.captcha.qcloud.com https://www.tycaptcha.com https://*.stripe.com https://checkout.airwallex.com https://checkout-demo.airwallex.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// CORSConfig contains allowed origins and the browser credential policy.
type CORSConfig struct {
	// AllowedOrigins is normalized by CORS. A wildcard takes precedence over
	// explicit origins and disables AllowCredentials for that middleware instance.
	AllowedOrigins []string `yaml:"allowed_origins"`
	// AllowCredentials permits browser credentials for matched explicit origins.
	// CORS forces it false when AllowedOrigins contains the wildcard.
	AllowCredentials bool `yaml:"allow_credentials"`
}

// CSPConfig supplies browser policy settings to the security-header middleware.
type CSPConfig struct {
	// Enabled publishes CSP on paths that SecurityHeaders treats as browser
	// pages. Baseline security headers are still emitted when CSP is disabled.
	Enabled bool `yaml:"enabled"`
	// Policy selects the CSP template. SecurityHeaders uses DefaultCSPPolicy
	// when blank and supplements configured sources for supported frontend integrations.
	Policy string `yaml:"policy"`
}
