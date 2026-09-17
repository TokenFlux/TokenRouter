package httpconfig

// 以下纯值定义 HTTP 参数，不读取配置或安装中间件。
type CORSConfig struct {
	AllowedOrigins   []string `mapstructure:"allowed_origins"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
}

type CSPConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Policy  string `mapstructure:"policy"`
}

const DefaultCSPPolicy = "default-src 'self'; worker-src 'self' blob:; script-src 'self' __CSP_NONCE__ https://accounts.google.com/gsi/client https://challenges.cloudflare.com https://*.alicdn.com https://static.cloudflareinsights.com https://turing.captcha.qcloud.com https://turing.captcha.gtimg.com https://ca.turing.captcha.qcloud.com https://global.turing.captcha.gtimg.com https://www.tycaptcha.com https://cloudcache.tencentcs.com https://*.stripe.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; style-src 'self' 'unsafe-inline' https://*.captcha.gtimg.com https://accounts.google.com/gsi/style https://fonts.googleapis.com https://*.alicdn.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; img-src 'self' data: blob: https:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' http://127.0.0.1:43110 http://127.0.0.1:43111 http://127.0.0.1:43112 http://127.0.0.1:43113 http://127.0.0.1:43114 http://127.0.0.1:43115 http://127.0.0.1:43116 http://127.0.0.1:43117 http://127.0.0.1:43118 http://127.0.0.1:43119 https://accounts.google.com/gsi/ https://turing.captcha.qcloud.com https://www.tycaptcha.com https://rce.tencentrio.com https:; frame-src https://accounts.google.com/gsi/ https://challenges.cloudflare.com https://turing.captcha.qcloud.com https://ca.turing.captcha.qcloud.com https://www.tycaptcha.com https://*.stripe.com https://checkout.airwallex.com https://checkout-demo.airwallex.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
