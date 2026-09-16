package service
import("sync";"testing")
// 复用生产上下行调用的两个方法，以同一开始屏障固定并发读写。
func TestS09PlanningWSUsageModelConcurrentDirections(t *testing.T){
 m:=newOpenAIWSPassthroughUsageMeta("first",nil);start:=make(chan struct{});var wg sync.WaitGroup;wg.Add(2)
 go func(){defer wg.Done();<-start;for i:=0;i<10000;i++{m.updateSessionRequestModel([]byte(`{"type":"session.update","session":{"model":"next"}}`))}}()
 go func(){defer wg.Done();<-start;for i:=0;i<10000;i++{_ = m.requestModelForFrame(nil)}}()
 close(start);wg.Wait()
}
