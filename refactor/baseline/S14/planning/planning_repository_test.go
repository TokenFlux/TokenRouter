//go:build integration

package repository

import (
 "context"
 "database/sql"
 "strings"
 "testing"
 "time"

 "github.com/TokenFlux/TokenRouter/internal/config"
 "github.com/TokenFlux/TokenRouter/internal/service"
 tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// 系统锁只使用测试框架的隔离数据库，不接触生产数据。
func TestS14PlanningB05SystemLockOwnership(t *testing.T){
 ctx:=context.Background()
 repo:=NewIdempotencyRepository(integrationEntClient,integrationDB)
 for _,scenario:=range []string{"reclaim-renew","late-release"}{t.Run(scenario,func(t *testing.T){
  _,err:=integrationDB.ExecContext(ctx,"DELETE FROM idempotency_records WHERE scope=$1","admin.system.operations.global_lock");if err!=nil{t.Fatal(err)}
  svc:=service.NewSystemOperationLockService(repo,service.IdempotencyConfig{ProcessingTimeout:time.Hour,SystemOperationTTL:2*time.Hour})
  first,err:=svc.Acquire(ctx,"s14-first");if err!=nil{t.Fatal(err)}
  defer func(){_ = svc.Release(ctx,first,false,"cleanup")}()
  _,err=integrationDB.ExecContext(ctx,"UPDATE idempotency_records SET locked_until=NOW()-INTERVAL '1 second' WHERE scope=$1","admin.system.operations.global_lock");if err!=nil{t.Fatal(err)}
  second,err:=svc.Acquire(ctx,"s14-second");if err!=nil{t.Fatal(err)}
  defer func(){_ = svc.Release(ctx,second,true,"")}()
  hash:=service.HashIdempotencyKey("global-system-operation-lock")
  current,err:=repo.GetByScopeAndKeyHash(ctx,"admin.system.operations.global_lock",hash);if err!=nil{t.Fatal(err)}
  if scenario=="reclaim-renew"{
   ok,err:=repo.ExtendProcessingLock(ctx,current.ID,second.OperationID(),time.Now().Add(time.Hour),time.Now().Add(2*time.Hour));if err!=nil{t.Fatal(err)}
   if !ok{t.Errorf("新持有者不能续租；数据库指纹仍为 %s",current.RequestFingerprint)}
  }else{
   if err:=svc.Release(ctx,first,true,"");err!=nil{t.Fatal(err)}
   current,err=repo.GetByScopeAndKeyHash(ctx,"admin.system.operations.global_lock",hash);if err!=nil{t.Fatal(err)}
   if current.Status!=service.IdempotencyStatusProcessing{t.Errorf("旧持有者释放覆盖新锁: %s",current.Status)}
  }
 })}
}

// 用独立 PostgreSQL 与本机 psql 检查真实进程退出码，不依赖模拟返回。
func TestS14PlanningB04RestoreSQLFailure(t *testing.T){
 ctx,cancel:=context.WithTimeout(context.Background(),2*time.Minute);defer cancel()
 pg,err:=tcpostgres.Run(ctx,"postgres:18.1-alpine3.23",tcpostgres.WithDatabase("s14_restore"),tcpostgres.WithUsername("postgres"),tcpostgres.WithPassword("postgres"),tcpostgres.BasicWaitStrategies());if err!=nil{t.Fatal(err)}
 defer func(){_ = pg.Terminate(context.Background())}()
 host,err:=pg.Host(ctx);if err!=nil{t.Fatal(err)}
 port,err:=pg.MappedPort(ctx,"5432/tcp");if err!=nil{t.Fatal(err)}
 dsn,err:=pg.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=sql.Open("postgres",dsn);if err!=nil{t.Fatal(err)};defer func(){_=db.Close()}()
 _,err=db.ExecContext(ctx,"CREATE TABLE fixture(id INT PRIMARY KEY); INSERT INTO fixture VALUES(1)");if err!=nil{t.Fatal(err)}
 dumper:=NewPgDumper(&config.Config{Database:config.DatabaseConfig{Host:host,Port:port.Int(),User:"postgres",Password:"postgres",DBName:"s14_restore",SSLMode:"disable"}})
 err=dumper.Restore(ctx,strings.NewReader("DELETE FROM fixture; INSERT INTO definitely_missing_table VALUES(1); INSERT INTO fixture VALUES(2);"))
 var count int;if e:=db.QueryRowContext(ctx,"SELECT COUNT(*) FROM fixture WHERE id=1").Scan(&count);e!=nil{t.Fatal(e)}
 t.Logf("原行保留=%d，Restore error=%v",count,err)
 if err==nil{t.Error("SQL 失败却报告恢复成功")}
 if count!=1{t.Error("失败恢复未保持原数据库")}
}
