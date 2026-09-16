// 版本比较保持旧容错解析，不引入新的 semver 规则。
package clientmeta

import (
	"regexp"
	"strconv"
	"strings"
)

// CompareVersions 比较两个 semver 版本号
// 返回: -1 (a < b), 0 (a == b), 1 (a > b)
func CompareVersions(a, b string) int {
	aParts := parseSemver(a)
	bParts := parseSemver(b)
	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

// parseSemver 解析 semver 版本号为 [major, minor, patch]
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}

var claudeUAVersionPattern = regexp.MustCompile(`(?i)^claude-cli/(\d+\.\d+\.\d+)`)

func ExtractClaudeCLIVersion(ua string) string {
	matches := claudeUAVersionPattern.FindStringSubmatch(ua)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
