// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

// RegistrationEmailSuffix 委托所属模块的唯一实现。
func RegistrationEmailSuffix(email string) string { return identity.RegistrationEmailSuffix(email) }

// RegistrationEmailDomain 委托所属模块的唯一实现。
func RegistrationEmailDomain(email string) string { return identity.RegistrationEmailDomain(email) }

// NormalizeRegistrationEmailDomain 委托所属模块的唯一实现。
func NormalizeRegistrationEmailDomain(domain string) string {
	return identity.NormalizeRegistrationEmailDomain(domain)
}

// NormalizeRegistrationEmailAddress 委托所属模块的唯一实现。
func NormalizeRegistrationEmailAddress(email string) string {
	return identity.NormalizeRegistrationEmailAddress(email)
}

// IsRegistrationEmailSuffixAllowed 委托所属模块的唯一实现。
func IsRegistrationEmailSuffixAllowed(email string, whitelist []string) bool {
	return identity.IsRegistrationEmailSuffixAllowed(email, whitelist)
}

// IsRegistrationEmailSuffixLimited 委托所属模块的唯一实现。
func IsRegistrationEmailSuffixLimited(email string, whitelist []string) bool {
	return identity.IsRegistrationEmailSuffixLimited(email, whitelist)
}

// NormalizeRegistrationEmailSuffixWhitelist 委托所属模块的唯一实现。
func NormalizeRegistrationEmailSuffixWhitelist(raw []string) ([]string, error) {
	return identity.NormalizeRegistrationEmailSuffixWhitelist(raw)
}

// ParseRegistrationEmailSuffixWhitelist 委托所属模块的唯一实现。
func ParseRegistrationEmailSuffixWhitelist(raw string) []string {
	return identity.ParseRegistrationEmailSuffixWhitelist(raw)
}
