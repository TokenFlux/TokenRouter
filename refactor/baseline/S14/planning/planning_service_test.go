//go:build unit

package service

import (
 "bytes"
 "compress/gzip"
 "context"
 "errors"
 "io"
 "os"
 "path/filepath"
 "sync"
 "testing"
 "time"
)

// 规划夹具只观察原实现，全部外部文件均使用测试临时目录。
func TestS14PlanningB01BackupRepeatedStart(t *testing.T) {
 s := newTestBackupService(t, newMockSettingRepo(), &mockDumper{}, newMockObjectStore())
 s.Start(); first := s.cronSched
 s.Start(); second := s.cronSched
 defer func(){ <-first.Stop().Done(); <-second.Stop().Done(); s.Stop() }()
 if first != second { t.Error("重复 Start 创建了第二个 cron 实例") }
 s.Stop(); s.Start(); third := s.cronSched
 defer func(){ <-third.Stop().Done() }()
 if third != second { t.Error("Stop 后 Start 重新启动 cron") }
}

type s14BlockingSettings struct {
 *mockSettingRepo
 entered chan struct{}
 release chan struct{}
 once sync.Once
 cancelled chan struct{}
}
func (r *s14BlockingSettings) GetValue(ctx context.Context,key string)(string,error){
 r.once.Do(func(){close(r.entered)})
 select {case <-ctx.Done(): close(r.cancelled);return "",ctx.Err();case <-r.release:return "",nil}
}
func TestS14PlanningB02StopCancelsWarmup(t *testing.T){
 base:=newMockSettingRepo()
 s:=newTestBackupService(t,base,&mockDumper{},newMockObjectStore())
 r:=&s14BlockingSettings{mockSettingRepo:base,entered:make(chan struct{}),release:make(chan struct{}),cancelled:make(chan struct{})}
 s.settingRepo=r
 started:=make(chan struct{});go func(){s.Start();close(started)}()
 <-r.entered
 s.Stop()
 select {case <-r.cancelled:case <-time.After(80*time.Millisecond):t.Error("Stop 返回后启动回源仍未取消")}
 close(r.release);<-started
}

func TestS14PlanningB03LocalStoreSymlink(t *testing.T){
 root,outside:=t.TempDir(),t.TempDir()
 if err:=os.WriteFile(filepath.Join(outside,"fixture"),[]byte("outside-only"),0600);err!=nil{t.Fatal(err)}
 if err:=os.Symlink(outside,filepath.Join(root,"link"));err!=nil{t.Fatal(err)}
 s:=NewLocalBackupStore(root)
 r,err:=s.Download(context.Background(),"link/fixture")
 if err==nil{b,_:=io.ReadAll(r);_ = r.Close();t.Errorf("读取了根外文件: %q",b)}
 _,err=s.Upload(context.Background(),"link/new",bytes.NewBufferString("escaped"),"")
 if err==nil{t.Error("写入了根外文件")}
 if err=s.Delete(context.Background(),"link/fixture");err==nil{t.Error("删除了根外文件")}
}

type s14FailSaveSettings struct{*mockSettingRepo}
func(r *s14FailSaveSettings)Set(context.Context,string,string)error{return errors.New("planned record write failure")}
type s14RestoreDumper struct{called chan struct{}}
func(d *s14RestoreDumper)Dump(context.Context,BackupDumpOptions)(io.ReadCloser,error){return nil,errors.New("unused")}
func(d *s14RestoreDumper)Restore(context.Context,io.Reader)error{close(d.called);return nil}
func TestS14PlanningB06RestoreMustRegisterBeforeExecution(t *testing.T){
 repo:=newMockSettingRepo();store:=newMockObjectStore();d:=&s14RestoreDumper{called:make(chan struct{})}
 s:=newTestBackupService(t,repo,d,store);seedS3Config(t,repo)
 var payload bytes.Buffer;gz:=gzip.NewWriter(&payload);_,_=gz.Write([]byte("SELECT 1;"));_=gz.Close()
 store.objects["restore.sql.gz"]=payload.Bytes()
 if err:=s.saveRecord(context.Background(),&BackupRecord{ID:"planned",Status:"completed",StorageType:"s3",StorageKey:"restore.sql.gz"});err!=nil{t.Fatal(err)}
 s.settingRepo=&s14FailSaveSettings{repo}
 _,err:=s.StartRestore(context.Background(),"planned")
 s.wg.Wait();s.Stop()
 if err==nil{t.Error("恢复记录写入失败仍接受恢复")}
 select{case <-d.called:t.Error("恢复记录写入失败仍执行数据库恢复");default:}
}
