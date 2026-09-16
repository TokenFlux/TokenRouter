// 固定设置页地址和响应重试指示属于 Ollama 供应商，不开放任意抓取。
package ollama

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const SettingsURL = "https://ollama.com/settings"
const RequestTimeout = 15 * time.Second
const MaxBodyBytes = 512 * 1024

var ErrUnauthorizedHTML = errors.New("settings HTML is a sign-in page")

func IsExactSettingsURL(parsed *url.URL) bool {
	return parsed != nil && parsed.Scheme == "https" && parsed.Host == "ollama.com" && parsed.Path == "/settings" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == ""
}

// RetryAfter 解析上游限流响应要求的最短重试间隔。
func RetryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
