package egress

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

var ErrMonitorURLInvalid = errors.New("invalid monitor URL")

// MonitorHostPolicy 保留后台监控的官方主机优先与自定义主机显式 allowlist 约束。
type MonitorHostPolicy struct {
	Enabled, AllowInsecureHTTP, AllowPrivate bool
	AllowedHosts                             []string
}

func (p MonitorHostPolicy) Clone() MonitorHostPolicy {
	p.AllowedHosts = slices.Clone(p.AllowedHosts)
	return p
}
func (p MonitorHostPolicy) Validate(raw string, official func(string) bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return ErrMonitorURLInvalid
	}
	if official != nil && official(strings.ToLower(parsed.Hostname())) {
		return nil
	}
	if !p.Enabled {
		return errors.New("CN_USAGE_MONITOR_CUSTOM_HOST_NOT_ALLOWLISTED")
	}
	_, err = ValidateHTTPURL(raw, p.AllowInsecureHTTP, ValidationOptions{AllowedHosts: p.AllowedHosts, RequireAllowlist: true, AllowPrivate: p.AllowPrivate})
	if err != nil {
		return fmt.Errorf("CN_USAGE_MONITOR_CUSTOM_HOST_NOT_ALLOWLISTED: %w", err)
	}
	return nil
}
