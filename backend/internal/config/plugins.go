package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// plugins.go defines deployment settings for the single-instance plugin runtime.
// An explicit plugin directory wins over derived storage placement. Otherwise
// DATA_DIR selects the deployment data root, followed by the configured storage
// directory and the local data fallback. Normalization computes paths but
// never creates package directories or starts processes during configuration load.
//
// Distribution proxies and child-process proxies are separate settings because
// downloading an installation and executing its network requests have different
// owners. Defaults apply to omitted file fields; explicit environment references
// are resolved by the shared loader before plugin configuration is decoded. Development directories remain configured but inactive unless
// dev_mode is enabled, and the main enabled switch remains the emergency stop
// for both normal and development plugin execution.

// PluginsConfig controls plugin execution, local storage, and package distribution.
type PluginsConfig struct {
	Enabled bool `mapstructure:"enabled"` // global plugin availability switch
	// Dir defaults under DATA_DIR or the existing data directory when omitted; normalization preserves an explicitly supplied path.
	Dir               string `mapstructure:"dir"`
	OfficialIndexURL  string `mapstructure:"official_index_url"`  // signed official catalog location
	DownloadProxy     string `mapstructure:"download_proxy"`      // package distribution proxy URL
	DownloadURLPrefix string `mapstructure:"download_url_prefix"` // optional GitHub download prefix
	PluginHTTPProxy   string `mapstructure:"plugin_http_proxy"`   // explicit child HTTP proxy
	MaxRunning        int    `mapstructure:"max_running"`         // concurrent native plugin limit
	DevMode           bool   `mapstructure:"dev_mode"`            // development directory availability switch
	// DevDirs lists unpacked plugin directories; runtime loading ignores the list unless both Enabled and DevMode are true.
	DevDirs []string `mapstructure:"dev_dirs"`
}

// setPluginDefaults registers all plugin keys before Viper unmarshals configuration.
func setPluginDefaults(v *viper.Viper) {
	v.SetDefault("plugins.enabled", true)
	v.SetDefault("plugins.dir", "")
	v.SetDefault("plugins.official_index_url", "https://raw.githubusercontent.com/TokenFlux/tokenrouter-plugins/dist/v1/index.json")
	v.SetDefault("plugins.download_proxy", "")
	v.SetDefault("plugins.download_url_prefix", "")
	v.SetDefault("plugins.plugin_http_proxy", "")
	v.SetDefault("plugins.max_running", 20)
	v.SetDefault("plugins.dev_mode", false)
	v.SetDefault("plugins.dev_dirs", []string{})
}

// normalize trims deployment strings and derives an omitted plugin storage directory.
// Explicit directory values retain precedence. The fallback reads DATA_DIR but
// performs no filesystem probes, writes, permission changes, or process startup.
func (p *PluginsConfig) normalize(dataDir string) {
	p.Dir = strings.TrimSpace(p.Dir)
	if p.Dir == "" {
		base := strings.TrimSpace(os.Getenv("DATA_DIR"))
		if base == "" {
			base = strings.TrimSpace(dataDir)
		}
		if base == "" {
			base = "./data"
		}
		p.Dir = filepath.Join(base, "plugins")
	}
	p.OfficialIndexURL = strings.TrimSpace(p.OfficialIndexURL)
	p.DownloadProxy = strings.TrimSpace(p.DownloadProxy)
	p.DownloadURLPrefix = strings.TrimSpace(p.DownloadURLPrefix)
	p.PluginHTTPProxy = strings.TrimSpace(p.PluginHTTPProxy)
	p.DevDirs = normalizeStringSlice(p.DevDirs)
}

// validate checks plugin deployment bounds and URL schemes without making requests.
// Disabled execution permits a zero process limit. Optional proxy strings may
// be empty, and diagnostics identify only the setting name rather than credentials.
func (p PluginsConfig) validate() error {
	if p.MaxRunning < 0 || (p.Enabled && p.MaxRunning == 0) {
		return fmt.Errorf("plugins.max_running must be positive when plugins are enabled")
	}
	for name, value := range map[string]string{"official_index_url": p.OfficialIndexURL, "download_url_prefix": p.DownloadURLPrefix} {
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("plugins.%s must be an HTTPS URL without credentials or a fragment", name)
		}
	}
	for name, value := range map[string]string{"download_proxy": p.DownloadProxy, "plugin_http_proxy": p.PluginHTTPProxy} {
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
			return fmt.Errorf("plugins.%s must use an HTTP, HTTPS, or SOCKS5 proxy", name)
		}
	}
	return nil
}
