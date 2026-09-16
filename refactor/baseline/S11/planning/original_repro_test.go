//go:build unit

package service

import (
 "context"
 "sync"
 "sync/atomic"
 "testing"
 "time"
 "github.com/TokenFlux/TokenRouter/internal/model"
 coderws "github.com/coder/websocket"
)

// 规划复现只覆盖原实现，不替换生产代码。
func TestS11PlanningWorkerStopWaitsInline(t *testing.T) {
 p := NewUsageRecordWorkerPoolWithOptions(UsageRecordWorkerPoolOptions{WorkerCount:1,QueueSize:1,TaskTimeout:time.Second,OverflowPolicy:"sync"})
 first := make(chan struct{}); poolRelease := make(chan struct{}); inline := make(chan struct{}); inlineRelease := make(chan struct{}); submitted := make(chan struct{})
 p.Submit(func(context.Context){close(first); <-poolRelease}); <-first
 p.Submit(func(context.Context){<-poolRelease})
 go func(){p.Submit(func(context.Context){close(inline); <-inlineRelease});close(submitted)}()
 <-inline
 stopped := make(chan struct{}); go func(){p.Stop();close(stopped)}()
 close(poolRelease)
 select {case <-stopped:t.Error("Stop returned while confirmed synchronous completion remained active"); case <-time.After(100*time.Millisecond):}
 close(inlineRelease); <-submitted; <-stopped
}
func TestS11PlanningWorkerStartAfterStop(t *testing.T) {
 p := NewUsageRecordWorkerPoolWithOptions(UsageRecordWorkerPoolOptions{WorkerCount:1,QueueSize:1,AutoScaleEnabled:true,AutoScaleMinWorkers:1,AutoScaleMaxWorkers:2})
 p.Stop(); p.Start()
 if p.autoScaleCancel != nil {p.autoScaleCancel();p.lifecycleWg.Wait();t.Error("Start after Stop created a new autoscale worker")}
}

type s11PlanningRuleRepo struct {
 mockErrorPassthroughRepo
 mu sync.Mutex
 current *model.ErrorPassthroughRule
 calls atomic.Int32
 captured chan struct{}
 release chan struct{}
 blockFirst bool
}
func (r *s11PlanningRuleRepo) List(ctx context.Context)([]*model.ErrorPassthroughRule,error){
 r.mu.Lock(); snapshot:=*r.current; r.mu.Unlock()
 if r.blockFirst && r.calls.Add(1)==1 {close(r.captured); select{case <-r.release:case <-ctx.Done():return nil,ctx.Err()}}
 return []*model.ErrorPassthroughRule{&snapshot},nil
}
func (r *s11PlanningRuleRepo) Update(_ context.Context,rule *model.ErrorPassthroughRule)(*model.ErrorPassthroughRule,error){r.mu.Lock(); defer r.mu.Unlock(); copy:=*rule;r.current=&copy;return &copy,nil}
func s11Rule()*model.ErrorPassthroughRule{m:="old";return &model.ErrorPassthroughRule{ID:1,Name:"fixture",Enabled:true,MatchMode:"any",ErrorCodes:[]int{503},PassthroughCode:true,CustomMessage:&m}}
func TestS11PlanningRuleOldReadAfterUpdate(t *testing.T){
 r:=&s11PlanningRuleRepo{current:s11Rule(),captured:make(chan struct{}),release:make(chan struct{}),blockFirst:true}
 s:=NewErrorPassthroughService(r,nil)
 done:=make(chan error,1);go func(){done<-s.reloadRulesFromDB(context.Background())}();<-r.captured
 changed:=*r.current;changed.Enabled=false
 if _,err:=s.Update(context.Background(),&changed);err!=nil{t.Fatal(err)}
 close(r.release);if err:=<-done;err!=nil{t.Fatal(err)}
 if rule:=s.MatchRule("openai",503,nil);rule!=nil{t.Error("completed disable was overwritten by an older in-flight read")}
}
func TestS11PlanningRuleSnapshotOwnership(t *testing.T){
 t.Run("input",func(t *testing.T){s:=NewErrorPassthroughService(nil,nil);r:=s11Rule();s.setLocalCache([]*model.ErrorPassthroughRule{r});*r.CustomMessage="outside";if got:=s.MatchRule("openai",503,nil);got!=nil&&*got.CustomMessage!="old"{t.Error("cache aliases caller-owned response pointer")}})
 t.Run("output",func(t *testing.T){s:=NewErrorPassthroughService(nil,nil);s.setLocalCache([]*model.ErrorPassthroughRule{s11Rule()});a:=s.MatchRule("openai",503,nil);*a.CustomMessage="outside";if b:=s.MatchRule("openai",503,nil);b!=nil&&*b.CustomMessage!="old"{t.Error("match result mutates shared compiled rule")}})
}
func TestS11PlanningRuleStopCancelsLoad(t *testing.T){
 r:=&s11PlanningRuleRepo{current:s11Rule(),captured:make(chan struct{}),release:make(chan struct{}),blockFirst:true}
 s:=NewErrorPassthroughService(r,nil);started:=make(chan struct{});go func(){s.Start();close(started)}();<-r.captured
 stopped:=make(chan struct{});go func(){s.Stop();close(stopped)}()
 select{case <-stopped:case <-time.After(100*time.Millisecond):t.Error("Stop cannot cancel context-aware startup rule load")}
 close(r.release);<-started;<-stopped
}

// 把取消固定在已经取得控制权、尚未查询账号的交接点。
type s11PlanningLiveStore struct{liveTestStore; cancel context.CancelFunc}
func(s *s11PlanningLiveStore)ClaimLiveController(ctx context.Context,hash,controller,owner string)(bool,error){ok,err:=s.liveTestStore.ClaimLiveController(ctx,hash,controller,owner);s.cancel();return ok,err}
type s11PlanningLiveAccounts struct{AccountRepository;reads atomic.Int32}
func(r *s11PlanningLiveAccounts)GetByID(ctx context.Context,id int64)(*Account,error){r.reads.Add(1);return nil,ctx.Err()}
func TestS11PlanningLiveHandoffCancellation(t *testing.T){
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 record:=&LiveCallRecord{CallHash:"planning",Controller:LiveControllerPending,AccountID:7,ExpiresAt:time.Now().Add(time.Minute)}
 store:=&s11PlanningLiveStore{liveTestStore:liveTestStore{record:record},cancel:cancel}
 accounts:=&s11PlanningLiveAccounts{}
 s:=&OpenAIGatewayService{cache:store,accountRepo:accounts,liveObserverStopped:true}
 start:=time.Now();err:=s.ProxyLiveSideband(ctx,record,&coderws.Conn{})
 if err!=context.Canceled{t.Errorf("expected cancellation, got %v",err)}
 if time.Since(start)>=100*time.Millisecond{t.Error("cancelled Live handoff still slept for the observer interval")}
 if accounts.reads.Load()!=0{t.Errorf("cancelled Live handoff still queried execution account: %d",accounts.reads.Load())}
}
