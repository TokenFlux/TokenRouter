//go:build integration
package app_test
import (
 "context"
 "testing"
 "github.com/TokenFlux/TokenRouter/internal/repository"
 "github.com/TokenFlux/TokenRouter/internal/service"
 "github.com/stretchr/testify/require"
)
// 规划基线验证原有 Cyber warning 与用户禁用仍处于同一数据库事务。
func TestS10PlanningModerationTransactionBaseline(t *testing.T){
 f:=newDatabaseFixture(t);ctx:=context.Background();repo:=repository.NewContentModerationRepository(f.db)
 u,e:=f.client.User.Create().SetEmail("s10-fixture@example.test").SetPasswordHash("fixture").Save(ctx);require.NoError(t,e)
 t.Run("ban-and-warning-commit",func(t *testing.T){
 w:=&service.ContentModerationCyberWarning{RequestID:"s10-success",UserID:&u.ID,WarningText:"cyber_policy"}
 banned,e:=repo.CreateCyberWarningAndApplyUserBan(ctx,w,service.ContentModerationCyberWarningPolicy{AutoBanEnabled:true,BanThreshold:1,WindowHours:24});require.NoError(t,e);require.True(t,banned)
 var status string;require.NoError(t,f.db.QueryRowContext(ctx,"SELECT status FROM users WHERE id=$1",u.ID).Scan(&status));require.Equal(t,"disabled",status)
 require.Positive(t,w.ID);require.True(t,w.AutoBanned)
 })
 t.Run("policy-update-failure-rolls-back-user-and-warning",func(t *testing.T){
 _,e:=f.db.ExecContext(ctx,"UPDATE users SET status='active' WHERE id=$1",u.ID);require.NoError(t,e)
 _,e=f.db.ExecContext(ctx,`CREATE FUNCTION s10_reject_warning_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.request_id='s10-rollback' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER s10_reject_warning_update BEFORE UPDATE ON content_moderation_cyber_warnings FOR EACH ROW EXECUTE FUNCTION s10_reject_warning_update();`);require.NoError(t,e)
 w:=&service.ContentModerationCyberWarning{RequestID:"s10-rollback",UserID:&u.ID,WarningText:"cyber_policy"}
 _,e=repo.CreateCyberWarningAndApplyUserBan(ctx,w,service.ContentModerationCyberWarningPolicy{AutoBanEnabled:true,BanThreshold:1,WindowHours:24});require.Error(t,e)
 var status string;require.NoError(t,f.db.QueryRowContext(ctx,"SELECT status FROM users WHERE id=$1",u.ID).Scan(&status));require.Equal(t,"active",status)
 var n int;require.NoError(t,f.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM content_moderation_cyber_warnings WHERE request_id='s10-rollback'").Scan(&n));require.Zero(t,n)
 })
}
