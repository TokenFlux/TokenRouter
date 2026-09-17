//go:build unit
package service

import (
 "context"
 "errors"
 "runtime"
 "testing"
 "time"
 "github.com/TokenFlux/TokenRouter/internal/config"
 "github.com/stretchr/testify/require"
)

// 规划夹具仅放在仓库外，通过 overlay 验证原实现。
func TestS13StoppedRuntimesRejectStart(t *testing.T) {
 t.Run("creative",func(t *testing.T){
  q:=&parallelCreativeQueue{ready:make(chan string)}
  w:=NewCreativeRunWorker(q,nil,nil,nil,nil,CreativeWorkerOptions{})
  r:=NewCreativeWorkerRuntime(w,&config.Config{Creative:config.CreativeConfig{QueueEnabled:true}})
  r.SetWorkerCount(1);r.Stop();r.Start();defer r.Stop()
  require.False(t,r.Running(),"停止后的创作台 runtime 不应重开")
 })
 t.Run("batchimage",func(t *testing.T){
  r:=NewBatchImageWorkerRuntime(NewBatchImageWorker(&blockingBatchImageRuntimeQueue{},&fakeBatchImageProcessor{},BatchImageWorkerOptions{}),&config.Config{BatchImage:config.BatchImageConfig{QueueEnabled:true}})
  r.Stop();r.Start();defer r.Stop()
  require.False(t,r.Running(),"停止后的批量图片 runtime 不应重开")
 })
}

type s13BlockedQueue struct { blockingBatchImageRuntimeQueue; entered, cancelled, release chan struct{} }
func (q *s13BlockedQueue) Reserve(ctx context.Context,_ time.Duration)(ReservedBatchImageJob,error){
 close(q.entered);<-ctx.Done();close(q.cancelled);<-q.release;return ReservedBatchImageJob{},ctx.Err()
}
func TestS13RepeatedStopWaitsForSameWork(t *testing.T){
 q:=&s13BlockedQueue{entered:make(chan struct{}),cancelled:make(chan struct{}),release:make(chan struct{})}
 r:=NewBatchImageWorkerRuntime(NewBatchImageWorker(q,&fakeBatchImageProcessor{},BatchImageWorkerOptions{}),&config.Config{BatchImage:config.BatchImageConfig{QueueEnabled:true}})
 r.Start();<-q.entered
 first:=make(chan struct{});go func(){r.Stop();close(first)}();<-q.cancelled
 second:=make(chan struct{});go func(){r.Stop();close(second)}()
 returned:=false
 select{case <-second:returned=true;case <-time.After(30*time.Millisecond):}
 close(q.release);<-first;<-second
 require.False(t,returned,"第一次停止仍有在途工作时，重复 Stop 不能报告完成")
}

type s13CleanupRepo struct { BatchImageRepository }
func (*s13CleanupRepo) ListBatchImageJobsDueForInputCleanup(ctx context.Context,_ time.Time,_ int)([]*BatchImageJob,error){return nil,ctx.Err()}
func (*s13CleanupRepo) ListBatchImageJobsDueForOutputCleanup(ctx context.Context,_ time.Time,_ int)([]*BatchImageJob,error){return nil,ctx.Err()}
func TestS13CleanupImmediateStop(t *testing.T){
 old:=runtime.GOMAXPROCS(1);defer runtime.GOMAXPROCS(old)
 s:=&BatchImageCleanupService{Repo:&s13CleanupRepo{},Config:&config.Config{BatchImage:config.BatchImageConfig{Enabled:true}}}
 s.Start();s.Stop()
}
func TestS13CreativeOutputFailureMustNotInferAgain(t *testing.T){
 f:=newCreativeWorkerFixture();id:="crun_s13_output_failure";seedCreativeRun(f,id,true)
 f.store.saveOutputErr=errors.New("redis unavailable")
 f.exec.result=&CreativeExecuteResult{Outputs:[]CreativeOutput{{Index:0,Bytes:[]byte("img"),Mime:"image/png"}},AccountID:55}
 _,err:=f.worker.process(context.Background(),id);require.NoError(t,err)
 f.store.saveOutputErr=nil
 _,err=f.worker.process(context.Background(),id);require.NoError(t,err)
 require.Equal(t,1,f.exec.calls,"provider 已成功，保存输出失败后的恢复不能再次推理")
}
