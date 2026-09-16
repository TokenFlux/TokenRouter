package handler
import (
 "context"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"
 "github.com/TokenFlux/TokenRouter/internal/service"
 "github.com/gin-gonic/gin"
 "github.com/stretchr/testify/require"
)
type s10PageSettings struct {service.SettingRepository}
func (s10PageSettings)GetValue(context.Context,string)(string,error){return `[{"page_slug":"guide","visibility":"user"}]`,nil}
func TestS10PlanningPageSymlinkEscape(t *testing.T){
 root:=t.TempDir();h:=NewPageHandler(root,service.NewSettingService(s10PageSettings{},nil));outside:=filepath.Join(t.TempDir(),"outside.md")
 require.NoError(t,os.WriteFile(outside,[]byte("private-fixture"),0600));require.NoError(t,os.Symlink(outside,filepath.Join(root,"pages","guide.md")))
 w:=httptest.NewRecorder();c,_:=gin.CreateTestContext(w);c.Request=httptest.NewRequest("GET","/api/v1/pages/guide",nil);c.Params=gin.Params{{Key:"slug",Value:"guide"}}
 h.GetPageContent(c)
 require.Equal(t,http.StatusNotFound,w.Code,"已配置的页面符号链接可越出 pages 根目录，返回 %q",w.Body.String())
}
