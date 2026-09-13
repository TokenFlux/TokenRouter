// SanitizeUpstreamQueries 保留旧执行层的查询凭据遮罩边界。
package logredact

import "regexp"

var upstreamQueryPattern = regexp.MustCompile(`(?i)([?&](?:key|client_secret|access_token|refresh_token)=)[^&"\s]+`)

func SanitizeUpstreamQueries(msg string) string {
	if msg == "" {
		return msg
	}
	return upstreamQueryPattern.ReplaceAllString(msg, `$1***`)
}
