// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	"crypto/sha256"
	"encoding/hex"
)

func AuthCacheKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}
