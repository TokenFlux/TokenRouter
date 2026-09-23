package selection

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func (s *Compatible) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.schedulingGroups == nil {
		return nil, nil
	}
	return s.schedulingGroups(ctx, id)
}

func (s *Gemini) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupRepo == nil {
		return nil, nil
	}
	return s.groupRepo.GetByID(ctx, id)
}

func (s *Generic) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupRepo == nil {
		return nil, nil
	}
	return s.groupRepo.GetByID(ctx, id)
}
