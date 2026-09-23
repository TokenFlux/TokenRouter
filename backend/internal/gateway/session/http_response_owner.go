package session

import (
	"context"
	"strings"
)

// HTTPResponseOwnerReader 只读取既有续接归属，不参与账号选择或复制会话缓存。
type HTTPResponseOwnerReader interface {
	GetHTTPResponseOwner(context.Context, int64, string) (int64, int64, bool, error)
}

// ValidateHTTPResponseOwner 保留同用户跨 Key 续接与历史只有 Key 归属的兼容规则。
// 无效输入在取得存储前短路，真实读取只执行一次。
func ValidateHTTPResponseOwner(ctx context.Context, read func() HTTPResponseOwnerReader, groupID int64, responseID string, userID, keyID int64) (bool, error) {
	if read == nil || strings.TrimSpace(responseID) == "" || userID <= 0 || keyID <= 0 {
		return false, nil
	}
	store := read()
	if store == nil {
		return false, nil
	}
	ownerUserID, ownerKeyID, found, err := store.GetHTTPResponseOwner(ctx, groupID, responseID)
	if err != nil || !found {
		return false, err
	}
	return ownerUserID == userID || (ownerUserID <= 0 && ownerKeyID == keyID), nil
}
