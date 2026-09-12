// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	strings "strings"
)

func OAuthFirstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func OAuthIsSafeLinuxDoSubject(subject string) bool {
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > OAuthLinuxDoMaxSubjectLength {
		return false
	}
	for _, r := range subject {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func OAuthLinuxDoSyntheticEmail(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	return "linuxdo-" + subject + LinuxDoConnectSyntheticEmailDomain
}

// OAuthLinuxDoMaxSubjectLength 保留合成邮箱本地部分的原长度边界。
const OAuthLinuxDoMaxSubjectLength = 64 - len("linuxdo-")
