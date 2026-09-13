// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	errors "errors"
	fmt "fmt"
)

type GroupExistenceLookup interface {
	GetByID(context.Context, int64) (*Group, error)
}
type GroupExistenceBatchReader interface {
	ExistsByIDs(context.Context, []int64) (map[int64]bool, error)
}

func ValidateGroupIDs(ctx context.Context, repo GroupExistenceLookup, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	if repo == nil {
		return errors.New("group repository not configured")
	}

	if batchReader, ok := repo.(GroupExistenceBatchReader); ok {
		existsByID, err := batchReader.ExistsByIDs(ctx, groupIDs)
		if err != nil {
			return fmt.Errorf("check groups exists: %w", err)
		}
		for _, groupID := range groupIDs {
			if groupID <= 0 || !existsByID[groupID] {
				return fmt.Errorf("get group: %w", ErrGroupNotFound)
			}
		}
		return nil
	}

	for _, groupID := range groupIDs {
		if _, err := repo.GetByID(ctx, groupID); err != nil {
			return fmt.Errorf("get group: %w", err)
		}
	}
	return nil
}
