package websearch
import (
 "context"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
 "github.com/redis/go-redis/v9"
 "github.com/stretchr/testify/require"
 tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)
func TestS10PlanningCanceledSearchRollsBackQuota(t *testing.T){
 setup,cancelSetup:=context.WithTimeout(context.Background(),time.Minute);defer cancelSetup()
 container,err:=tcredis.Run(setup,"redis:8.4-alpine");require.NoError(t,err)
 t.Cleanup(func(){require.NoError(t,container.Terminate(context.Background()))})
 uri,err:=container.ConnectionString(setup);require.NoError(t,err);opts,err:=redis.ParseURL(uri);require.NoError(t,err)
 rdb:=redis.NewClient(opts);defer rdb.Close()
 entered:=make(chan struct{});release:=make(chan struct{})
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){close(entered);<-release;w.WriteHeader(500)}));defer srv.Close()
 original:=*braveSearchURL;req,_:=http.NewRequest("GET",srv.URL,nil);*braveSearchURL=*req.URL;defer func(){*braveSearchURL=original}()
 m:=NewManager([]ProviderConfig{{Type:"brave",APIKey:"fixture",QuotaLimit:10}},rdb);m.clientCache[""]=srv.Client()
 ctx,cancel:=context.WithCancel(context.Background());done:=make(chan error,1)
 go func(){_,_,e:=m.SearchWithBestProvider(ctx,SearchRequest{Query:"fixture"});done<-e}()
 <-entered;before,e:=m.GetUsage(context.Background(),"brave");require.NoError(t,e);require.Equal(t,int64(1),before)
 cancel();err=<-done;close(release);require.Error(t,err)
 after,e:=m.GetUsage(context.Background(),"brave");require.NoError(t,e)
 require.Zero(t,after,"请求取消后，已确认预占的额度仍未回滚")
}
