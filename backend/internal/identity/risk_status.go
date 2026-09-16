// RiskStatusCommands 只执行风控已确定的用户状态意图，不改余额或认证策略。
package identity

import "context"

type RiskUser struct {
	ID                  int64
	Role, Status, Email string
}
type RiskStatusCommands struct{ users UserRepository }

func NewRiskStatusCommands(users UserRepository) *RiskStatusCommands {
	return &RiskStatusCommands{users: users}
}
func (s *RiskStatusCommands) Read(ctx context.Context, id int64) (*RiskUser, error) {
	u, e := s.users.GetByID(ctx, id)
	if u == nil {
		return nil, e
	}
	return &RiskUser{ID: u.ID, Role: u.Role, Status: u.Status, Email: u.Email}, e
}
func (s *RiskStatusCommands) SetStatus(ctx context.Context, id int64, status string) error {
	return s.users.Update(ctx, &User{ID: id, Status: status}, UserUpdateFields{Status: true})
}
