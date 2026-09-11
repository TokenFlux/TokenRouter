package clientmeta

import (
	"strings"

	"golang.org/x/mod/semver"
)

// IsSupportedCLIVersion 判断运维给的覆盖值是否可用。
//
// 判据有两条，缺一不可：
//  1. 严格三段纯数字（"2.1.251"）。带 -local / -dev / +build 等后缀的版本号会被
//     identity_service 的 fingerprintUserAgentPattern 拒绝，一旦漏进去，该账号的
//     持久指纹会被写成一个不存在的客户端版本，此后所有上游请求都声称这个版本，
//     被判非正版并持续 429——而系统内没有指纹重置入口。
//  2. 不低于内置基线 CLICurrentVersion。向下覆盖没有任何使用场景，
//     却会让 identity_service 的主版本超前检查基准跟着一起降。
func IsSupportedClaudeCLIVersion(version, minimum string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	// semver 允许 "v1.2" 与预发布/构建元数据，这里都不接受：
	// Canonical 相等可排除省略段，再显式排除预发布与构建元数据。
	canonical := "v" + version
	if !semver.IsValid(canonical) || semver.Canonical(canonical) != canonical {
		return false
	}
	if semver.Prerelease(canonical) != "" || semver.Build(canonical) != "" {
		return false
	}
	return semver.Compare(canonical, "v"+minimum) >= 0
}
