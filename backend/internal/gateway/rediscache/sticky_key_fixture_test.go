//go:build integration

package rediscache_test

import "fmt"

// 原集成断言直接检查持久化兼容键。
func buildSessionKey(groupID int64, hash string) string {
	return fmt.Sprintf("sticky_session:%d:%s", groupID, hash)
}
func buildSessionOwnerKey(userID int64, source, hash string) string {
	return fmt.Sprintf("sticky_session_owner:%d:%s:%s", userID, source, hash)
}
