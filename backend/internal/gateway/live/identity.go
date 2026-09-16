package live

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// Lookup 校验原 call、Key、付款用户和分组绑定，关闭记录仍视为未找到。
func (s *Service) Lookup(ctx context.Context, callID string, identity session.LiveCallIdentity) (*session.LiveCallRecord, error) {
	store, err := s.ports.Store()
	if err != nil {
		return nil, err
	}
	record, err := store.GetLiveCall(ctx, HashCallID(callID))
	if err != nil {
		return nil, err
	}
	if record.CallID != callID || record.APIKeyID != identity.APIKeyID || record.UserID != identity.UserID || record.GroupID != liveGroupID(identity.GroupID) {
		return nil, session.ErrLiveIdentityMismatch
	}
	if record.Controller == session.LiveControllerClosed {
		return nil, session.ErrLiveCallNotFound
	}
	return record, nil
}
