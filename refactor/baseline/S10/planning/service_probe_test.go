package service

import (
 "context"
 "errors"
 "net"
 "fmt"
 "sync/atomic"
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

// s10ReadBarrierRepo 固定两个请求均完成旧值读取的交错，不修改被测实现。
type s10ReadBarrierRepo struct {
 *notificationEmailMemorySettingRepo
 key string
 arrived chan struct{}
 release chan struct{}
 reads atomic.Int32
}
func (r *s10ReadBarrierRepo) GetValue(ctx context.Context, key string) (string,error) {
 v,err:=r.notificationEmailMemorySettingRepo.GetValue(ctx,key)
 if key==r.key && r.reads.Add(1)<=2 { r.arrived<-struct{}{}; <-r.release }
 return v,err
}
func TestS10PlanningConcurrentUnsubscribeTokens(t *testing.T) {
 r:=&s10ReadBarrierRepo{notificationEmailMemorySettingRepo:newNotificationEmailMemorySettingRepo(),key:notificationEmailUnsubscribeSecretKey,arrived:make(chan struct{},2),release:make(chan struct{})}
 svc:=NewNotificationEmailService(r,nil)
 results:=make(chan string,2); failures:=make(chan error,2)
 for range 2 { go func(){v,e:=svc.createUnsubscribeToken(context.Background(),"fixture@example.com",NotificationEmailEventBalanceLow);results<-v;failures<-e}() }
 <-r.arrived; <-r.arrived; close(r.release)
 tokens:=[]string{<-results,<-results};require.NoError(t,<-failures);require.NoError(t,<-failures)
 invalid:=0
 for _,token:=range tokens {if _,e:=svc.parseUnsubscribeToken(context.Background(),token);e!=nil {invalid++}}
 require.Zero(t,invalid,"同一进程并发生成的两个退订令牌必须都能验证")
}
func TestS10PlanningConcurrentDelivery(t *testing.T) {
 base:=newNotificationEmailMemorySettingRepo();smtpServer:=startNotificationEmailTestSMTPServer(t)
 require.NoError(t,base.SetMultiple(context.Background(),smtpServer.settings()))
 input:=NotificationEmailSendInput{Event:NotificationEmailEventSubscriptionExpiryReminder,RecipientEmail:"fixture@example.com",SourceType:"subscription",SourceID:"1",ReminderKey:"7d"}
 r:=&s10ReadBarrierRepo{notificationEmailMemorySettingRepo:base,key:notificationEmailDeliveryKey(input.Event,input.SourceType,input.SourceID,input.RecipientEmail,input.ReminderKey),arrived:make(chan struct{},2),release:make(chan struct{})}
 svc:=NewNotificationEmailService(r,NewEmailService(r,nil));done:=make(chan error,2)
 for range 2 {go func(){done<-svc.Send(context.Background(),input)}()}; <-r.arrived; <-r.arrived; close(r.release)
 require.NoError(t,<-done);require.NoError(t,<-done)
 require.Equal(t,int64(1),smtpServer.messageCount(),"同一个通知去重键并发调用不得重复发信")
}

// s10ConfigBarrierRepo 固定旧配置回源晚于保存完成，检验缓存最终值。
type s10ConfigBarrierRepo struct {
 *notificationEmailMemorySettingRepo
 reads atomic.Int32
 arrived chan struct{}
 release chan struct{}
}
func (r *s10ConfigBarrierRepo) GetValue(ctx context.Context,key string)(string,error){
 v,e:=r.notificationEmailMemorySettingRepo.GetValue(ctx,key)
 if key==SettingKeyWebSearchEmulationConfig && r.reads.Add(1)==1 {close(r.arrived);<-r.release}
 return v,e
}
func TestS10PlanningSearchOldLoadOverwritesSave(t *testing.T){
 webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{});webSearchEmulationSF.Forget(sfKeyWebSearchConfig)
 t.Cleanup(func(){webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{});webSearchEmulationSF.Forget(sfKeyWebSearchConfig)})
 r:=&s10ConfigBarrierRepo{notificationEmailMemorySettingRepo:newNotificationEmailMemorySettingRepo(),arrived:make(chan struct{}),release:make(chan struct{})}
 require.NoError(t,r.Set(context.Background(),SettingKeyWebSearchEmulationConfig,`{"enabled":false,"providers":[]}`))
 svc:=NewSettingService(r,nil);done:=make(chan error,1)
 go func(){_,e:=svc.GetWebSearchEmulationConfig(context.Background());done<-e}()
 <-r.arrived
 require.NoError(t,svc.SaveWebSearchEmulationConfig(context.Background(),&WebSearchEmulationConfig{Enabled:true,Providers:[]WebSearchProviderConfig{{Type:"brave",APIKey:"fixture"}}}))
 close(r.release);require.NoError(t,<-done)
 actual,e:=svc.GetWebSearchEmulationConfig(context.Background());require.NoError(t,e);require.True(t,actual.Enabled,"已保存的新配置被先开始的旧回源覆盖")
}
func TestS10PlanningSearchConfigIndependentCopies(t *testing.T){
 webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{});webSearchEmulationSF.Forget(sfKeyWebSearchConfig)
 t.Cleanup(func(){webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{});webSearchEmulationSF.Forget(sfKeyWebSearchConfig)})
 r:=newNotificationEmailMemorySettingRepo();svc:=NewSettingService(r,nil)
 q:=int64(100);input:=&WebSearchEmulationConfig{Enabled:true,Providers:[]WebSearchProviderConfig{{Type:"brave",APIKey:"fixture",QuotaLimit:&q}}}
 require.NoError(t,svc.SaveWebSearchEmulationConfig(context.Background(),input));input.Enabled=false;q=1
 v,e:=svc.GetWebSearchEmulationConfig(context.Background());require.NoError(t,e)
 require.True(t,v.Enabled,"保存后调用方仍可污染运行配置");require.Equal(t,int64(100),*v.Providers[0].QuotaLimit)
}
func TestS10PlanningCanceledEmailDoesNotSend(t *testing.T){
 r:=newNotificationEmailMemorySettingRepo();srv:=startNotificationEmailTestSMTPServer(t);require.NoError(t,r.SetMultiple(context.Background(),srv.settings()))
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 err:=NewEmailService(r,nil).SendEmail(ctx,"fixture@example.com","fixture","fixture")
 require.True(t,errors.Is(err,context.Canceled),"取消后的 SMTP 仍开始发送，err=%v messages=%d",err,srv.messageCount())
 require.Zero(t,srv.messageCount())
}
// 真实 TCP 连接停在 SMTP 问候读取，确认取消能否终止在途 I/O。
func TestS10PlanningSMTPInFlightCancellation(t *testing.T){
 ln,err:=net.Listen("tcp","127.0.0.1:0");require.NoError(t,err);defer ln.Close()
 accepted:=make(chan net.Conn,1)
 go func(){conn,e:=ln.Accept();if e==nil {accepted<-conn}}()
 r:=newNotificationEmailMemorySettingRepo();port:=ln.Addr().(*net.TCPAddr).Port
 require.NoError(t,r.SetMultiple(context.Background(),map[string]string{SettingKeySMTPHost:"127.0.0.1",SettingKeySMTPPort:fmt.Sprint(port),SettingKeySMTPFrom:"sender@example.com",SettingKeySMTPUsername:"fixture",SettingKeySMTPPassword:"fixture"}))
 ctx,cancel:=context.WithCancel(context.Background());defer cancel();done:=make(chan error,1)
 go func(){done<-NewEmailService(r,nil).SendEmail(ctx,"fixture@example.com","fixture","fixture")}()
 conn:=<-accepted;defer conn.Close();cancel()
 select {case e:=<-done:require.ErrorIs(t,e,context.Canceled)
 case <-time.After(250*time.Millisecond): _=conn.Close();<-done;t.Fatal("SMTP 在途问候读取未响应取消，需外部关闭连接才退出")}
}
