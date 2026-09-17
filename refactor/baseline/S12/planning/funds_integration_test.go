//go:build integration

package repository

import (
 "context"
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strconv"
 "sync/atomic"
 "testing"
 "time"
 dbent "github.com/TokenFlux/TokenRouter/ent"
 "github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
 "github.com/TokenFlux/TokenRouter/internal/payment"
 "github.com/TokenFlux/TokenRouter/internal/service"
 "github.com/stretchr/testify/require"
 stripe "github.com/stripe/stripe-go/v85"
)

// 隔离数据库与本地供应商；只调用公开生产入口。
func s12PlanningRefund(t *testing.T,status string)(*dbent.Client,*service.PaymentService,*dbent.PaymentOrder){
 t.Helper();ctx:=context.Background();c:=testEntClient(t)
 u,e:=c.User.Create().SetEmail("s12-refund@example.com").SetPasswordHash("test-hash").SetUsername("s12").SetBalance(100).Save(ctx);require.NoError(t,e)
 inst,e:=c.PaymentProviderInstance.Create().SetName("s12").SetProviderKey(payment.TypeStripe).SetConfig(`{"secretKey":"test-only","currency":"USD"}`).SetSupportedTypes(payment.TypeStripe).SetRefundEnabled(true).Save(ctx);require.NoError(t,e)
 o,e:=c.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName(u.Username).SetAmount(50).SetPayAmount(50).SetRechargeCode("s12-refund").SetOutTradeNo("s12-refund").SetPaymentType(payment.TypeStripe).SetPaymentTradeNo("pi_s12").SetOrderType(payment.OrderTypeBalance).SetStatus(status).SetRefundAmount(50).SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).SetClientIP("127.0.0.1").SetSrcHost("test.local").SetProviderInstanceID(strconv.FormatInt(inst.ID,10)).Save(ctx);require.NoError(t,e)
 t.Cleanup(func(){_,e:=c.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID,10))).Exec(context.Background());require.NoError(t,e);require.NoError(t,c.PaymentOrder.DeleteOneID(o.ID).Exec(context.Background()));require.NoError(t,c.PaymentProviderInstance.DeleteOneID(inst.ID).Exec(context.Background()))})
 s:=service.NewPaymentService(c,payment.NewRegistry(),payment.NewDefaultLoadBalancer(c,nil),nil,nil,nil,NewUserRepository(c,integrationDB),nil,nil)
 return c,s,o
}
func s12PlanningStripe(t *testing.T,h http.HandlerFunc){
 t.Helper();server:=httptest.NewServer(h);t.Cleanup(server.Close);original:=stripe.GetBackend(stripe.APIBackend)
 stripe.SetBackend(stripe.APIBackend,stripe.GetBackendWithConfig(stripe.APIBackend,&stripe.BackendConfig{URL:stripe.String(server.URL),HTTPClient:server.Client(),MaxNetworkRetries:stripe.Int64(0)}));t.Cleanup(func(){stripe.SetBackend(stripe.APIBackend,original)})
}
func s12PlanningFailAudit(t *testing.T,c *dbent.Client,id int64,action string){
 t.Helper();name:=fmt.Sprintf("s12_planning_audit_%d",id);ctx:=context.Background()
 _,e:=integrationDB.ExecContext(ctx,fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.order_id='%d' AND NEW.action='%s' THEN RAISE EXCEPTION 's12 forced audit failure'; END IF; RETURN NEW; END; $$`,name,id,action));require.NoError(t,e)
 _,e=integrationDB.ExecContext(ctx,fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON payment_audit_logs FOR EACH ROW EXECUTE FUNCTION %s()`,name,name));require.NoError(t,e)
 t.Cleanup(func(){_,e:=integrationDB.ExecContext(ctx,fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON payment_audit_logs; DROP FUNCTION IF EXISTS %s()",name,name));require.NoError(t,e)})
}
func TestS12PlanningStaleRefundFailureOverwritesSuccess(t *testing.T){
 ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel();c,s,o:=s12PlanningRefund(t,service.OrderStatusRefundPending)
 _,e:=c.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID,10)).SetAction("REFUND_PENDING").SetOperator("admin").SetDetail(`{"refundID":"re_s12","deductBalance":true,"balanceDeducted":50,"deductionRollbackOK":true}`).Save(ctx);require.NoError(t,e)
 entered:=make(chan struct{});release:=make(chan struct{});var calls atomic.Int32
 s12PlanningStripe(t,func(w http.ResponseWriter,r *http.Request){status:="succeeded";if calls.Add(1)==1{close(entered);select{case <-release:case <-r.Context().Done():return};status="failed"};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]string{"id":"re_s12","object":"refund","status":status})})
 done:=make(chan struct{});go func(){_,_=s.QueryAndFinalizeRefund(ctx,o.ID);close(done)}();<-entered
 result,e:=s.QueryAndFinalizeRefund(ctx,o.ID);require.NoError(t,e);require.True(t,result.Success);close(release);<-done
 current,e:=c.PaymentOrder.Get(ctx,o.ID);require.NoError(t,e);u,e:=c.User.Get(ctx,o.UserID);require.NoError(t,e)
 t.Logf("balance=%v final_status=%s",u.Balance,current.Status);require.Equal(t,service.OrderStatusRefunded,current.Status,"迟到失败不得覆盖已完成退款")
}
func TestS12PlanningPendingRefundAuditLossSkipsDeduction(t *testing.T){
 ctx:=context.Background();c,s,o:=s12PlanningRefund(t,service.OrderStatusCompleted)
 s12PlanningStripe(t,func(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");if r.Method==http.MethodPost{_,_=w.Write([]byte(`{"id":"re_s12","object":"refund","status":"pending"}`));return};_,_=w.Write([]byte(`{"object":"list","data":[{"id":"re_s12","object":"refund","status":"succeeded"}],"has_more":false}`))})
 s12PlanningFailAudit(t,c,o.ID,"REFUND_PENDING")
 plan,warning,e:=s.PrepareRefund(ctx,o.ID,50,"test",false,true);require.NoError(t,e);require.Nil(t,warning)
 result,e:=s.ExecuteRefund(ctx,plan);require.NoError(t,e);require.False(t,result.Success)
 result,e=s.QueryAndFinalizeRefund(ctx,o.ID);require.NoError(t,e);require.True(t,result.Success)
 u,e:=c.User.Get(ctx,o.UserID);require.NoError(t,e);t.Logf("confirmed refund deducted=%v balance=%v",result.BalanceDeducted,u.Balance)
 require.Equal(t,50.0,u.Balance,"缺失 pending 审计不能静默跳过权益回收")
}
func TestS12PlanningImmediateRefundAuditFailureIsNotReported(t *testing.T){
 ctx:=context.Background();c,s,o:=s12PlanningRefund(t,service.OrderStatusCompleted)
 s12PlanningStripe(t,func(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");_,_=w.Write([]byte(`{"id":"re_s12","object":"refund","status":"succeeded"}`))})
 s12PlanningFailAudit(t,c,o.ID,"REFUND_SUCCESS")
 plan,warning,e:=s.PrepareRefund(ctx,o.ID,50,"test",false,true);require.NoError(t,e);require.Nil(t,warning)
 result,e:=s.ExecuteRefund(ctx,plan);u,readErr:=c.User.Get(ctx,o.UserID);require.NoError(t,readErr);cur,readErr:=c.PaymentOrder.Get(ctx,o.ID);require.NoError(t,readErr)
 count,readErr:=c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID,10)),paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx);require.NoError(t,readErr)
 t.Logf("result=%+v error=%v balance=%v status=%s success_audits=%d",result,e,u.Balance,cur.Status,count)
 require.Error(t,e,"必须保留的退款成功审计写入失败不能返回成功")
}

// 只在真实查询返回后设屏障，模拟两个订单同时读取剩余返利上限。
type s12PlanningAffiliateBarrier struct{service.AffiliateRepository;ready chan struct{};calls atomic.Int32}
func(r *s12PlanningAffiliateBarrier)GetAccruedRebateFromInvitee(ctx context.Context,inviter,invitee int64)(float64,error){v,e:=r.AffiliateRepository.GetAccruedRebateFromInvitee(ctx,inviter,invitee);if e!=nil{return v,e};if r.calls.Add(1)==2{close(r.ready)};select{case <-r.ready:return v,nil;case <-ctx.Done():return 0,ctx.Err()}}
func TestS12PlanningAffiliateCapConcurrentOrders(t *testing.T){
 ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel();c:=testEntClient(t);repo:=NewAffiliateRepository(c,integrationDB)
 inviter,e:=c.User.Create().SetEmail("s12-inviter@example.com").SetPasswordHash("hash").Save(ctx);require.NoError(t,e)
 invitee,e:=c.User.Create().SetEmail("s12-invitee@example.com").SetPasswordHash("hash").Save(ctx);require.NoError(t,e)
 _,e=repo.EnsureUserAffiliate(ctx,inviter.ID);require.NoError(t,e);_,e=repo.EnsureUserAffiliate(ctx,invitee.ID);require.NoError(t,e);_,e=repo.BindInviter(ctx,invitee.ID,inviter.ID);require.NoError(t,e)
 sr:=NewSettingRepository(c);require.NoError(t,sr.SetMultiple(ctx,map[string]string{service.SettingKeyAffiliateEnabled:"true",service.SettingKeyAffiliateRebateRate:"100",service.SettingKeyAffiliateRebatePerInviteeCap:"10",service.SettingKeyAffiliateRebateFreezeHours:"0",service.SettingKeyAffiliateRebateDurationDays:"0"}))
 barrier:=&s12PlanningAffiliateBarrier{AffiliateRepository:repo,ready:make(chan struct{})};svc:=service.NewAffiliateService(barrier,service.NewSettingService(sr,nil),nil,nil);errs:=make(chan error,2)
 for range 2{go func(){_,e:=svc.AccrueInviteRebate(ctx,invitee.ID,8);errs<-e}()};require.NoError(t,<-errs);require.NoError(t,<-errs)
 amount,e:=repo.GetAccruedRebateFromInvitee(ctx,inviter.ID,invitee.ID);require.NoError(t,e);t.Logf("cap=10 accrued=%v",amount);require.LessOrEqual(t,amount,10.0,"并发计提不能超过单被邀请人上限")
}
