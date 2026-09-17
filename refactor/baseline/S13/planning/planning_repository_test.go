//go:build integration
package repository
import (
 "context"
 "testing"
 "time"
 "github.com/TokenFlux/TokenRouter/internal/service"
 "github.com/redis/go-redis/v9"
 "github.com/stretchr/testify/require"
)
// 同一进程的旧持有者与接管者通过真实 Redis 验证现有锁语义。
func TestS13BatchImageLostOwner(t *testing.T){
 t.Run("renewal_reports_loss",func(t *testing.T){
  ctx:=context.Background();db:=testRedis(t);q:=newBatchImageQueueWithOptions(db,batchImageQueueOptions{})
  old,ok,err:=q.TryAcquireJobLock(ctx,"imgbatch_s13",time.Minute);require.NoError(t,err);require.True(t,ok)
  require.NoError(t,db.Del(ctx,q.lockKey("imgbatch_s13")).Err())
  next,ok,err:=q.TryAcquireJobLock(ctx,"imgbatch_s13",time.Minute);require.NoError(t,err);require.True(t,ok);defer next.Release(ctx)
  err=old.(service.BatchImageJobLockRefresher).Refresh(ctx,time.Minute)
  require.Error(t,err,"旧 token 续期未命中，必须报告失去所有权")
 })
 t.Run("old_ack_preserves_successor",func(t *testing.T){
  ctx:=context.Background();db:=testRedis(t);q:=newBatchImageQueueWithOptions(db,batchImageQueueOptions{});id:="imgbatch_s13_ack"
  old,ok,err:=q.TryAcquireJobLock(ctx,id,time.Minute);require.NoError(t,err);require.True(t,ok);defer old.Release(ctx)
  require.NoError(t,db.Del(ctx,q.lockKey(id)).Err())
  next,ok,err:=q.TryAcquireJobLock(ctx,id,time.Minute);require.NoError(t,err);require.True(t,ok);defer next.Release(ctx)
  require.NoError(t,db.ZAdd(ctx,q.activeKey,redis.Z{Score:float64(time.Now().UnixMilli()),Member:id}).Err())
  require.NoError(t,db.Set(ctx,q.inflightKey(id),id,time.Hour).Err())
  require.NoError(t,q.Ack(ctx,id))
  _,err=db.ZScore(ctx,q.activeKey,id).Result()
  require.NoError(t,err,"旧 worker 的 ACK 不能删除接管者的 active 记录")
 })
}
