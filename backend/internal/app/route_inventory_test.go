package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/server"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// routeInventoryValue 只构造 handler 接收者和无副作用中间件；测试不执行用例或访问存储。
func routeInventoryValue(kind reflect.Type, depth int) reflect.Value {
	if kind.Kind() == reflect.Pointer {
		value := reflect.New(kind.Elem())
		if kind.Elem().Kind() == reflect.Struct && depth < 3 {
			for i := 0; i < value.Elem().NumField(); i++ {
				field := value.Elem().Field(i)
				if kind.Elem().Field(i).Anonymous && field.CanSet() {
					field.Set(routeInventoryValue(field.Type(), depth+1))
				}
			}
		}
		return value
	}
	if kind.Kind() == reflect.Func {
		return reflect.MakeFunc(kind, func([]reflect.Value) []reflect.Value {
			result := make([]reflect.Value, kind.NumOut())
			for i := range result {
				result[i] = reflect.Zero(kind.Out(i))
			}
			return result
		})
	}
	if kind.Kind() == reflect.Struct {
		value := reflect.New(kind).Elem()
		for i := 0; i < value.NumField(); i++ {
			if value.Field(i).CanSet() && value.Field(i).Kind() == reflect.Func {
				value.Field(i).Set(routeInventoryValue(value.Field(i).Type(), depth+1))
			}
		}
		return value
	}
	return reflect.Zero(kind)
}
func routeInventoryMount[T any](t *testing.T, factory any) T {
	value := reflect.ValueOf(factory)
	args := make([]reflect.Value, value.Type().NumIn())
	for i := range args {
		args[i] = routeInventoryValue(value.Type().In(i), 0)
	}
	result, ok := value.Call(args)[0].Interface().(T)
	require.True(t, ok, "生产注册函数的返回类型不匹配")
	return result
}

// TestS15NativeRouteInventory 对照固定的 693 条路由快照，实际调用生产注册函数并由 Gin 检测重复注册。
func TestS15NativeRouteInventory(t *testing.T) {

	r := gin.New()
	var chain []string
	r.Use(func(c *gin.Context) { chain = c.HandlerNames(); c.Abort() })
	v1 := r.Group("/api/v1")
	noop := func(c *gin.Context) { c.Next() }
	limiter := middleware.NewRateLimiter(nil)
	security := httpRouteSecurity{JWT: inventoryJWT, Admin: inventoryAdmin, Audit: inventoryAudit, StepUp: inventoryStepUp, BackendAuth: inventoryBackendAuth, BackendUser: inventoryBackendUser, Panel: middleware.NewPanelRateLimiter(limiter, nil), AuthLimiter: limiter}
	server.RegisterCommonRoutes(r)
	routeInventoryMount[authRouteMount](t, provideAuthRouteMount)(v1, security)
	routeInventoryMount[userRouteMount](t, provideUserRouteMount)(v1, security)
	routeInventoryMount[adminRouteMount](t, provideAdminRouteMount)(v1, security, noop)
	routeInventoryMount[gatewayRouteMount](t, provideGatewayRouteMount)(r)
	routeInventoryMount[paymentRouteMount](t, providePaymentRouteMount)(v1, security)
	raw, err := os.ReadFile("testdata/routes.json")
	require.NoError(t, err)
	var expected []string
	require.NoError(t, json.Unmarshal(raw, &expected))
	actual := make([]string, 0, len(r.Routes()))
	for _, route := range r.Routes() {
		actual = append(actual, route.Method+" "+route.Path)
		path := route.Path
		segments := strings.Split(path, "/")
		for i, part := range segments {
			if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
				segments[i] = "1"
			}
		}
		path = strings.Join(segments, "/")
		chain = nil
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(route.Method, path, nil))
		require.NotEmpty(t, chain, route.Method+" "+route.Path)
		position := func(suffix string) int {
			for i, name := range chain {
				if strings.HasSuffix(name, "."+suffix) {
					return i
				}
			}
			return -1
		}
		if strings.HasPrefix(route.Path, "/api/v1/admin/") {
			require.GreaterOrEqual(t, position("inventoryAdmin"), 0, route.Path)
			require.Greater(t, position("inventoryAudit"), position("inventoryAdmin"), route.Path)
		}
		for _, pair := range [][2]string{{"inventoryJWT", "inventoryBackendUser"}, {"inventoryBackendUser", "inventoryAudit"}, {"inventoryBackendAuth", "inventoryAudit"}, {"inventoryAdmin", "inventoryStepUp"}} {
			before, after := position(pair[0]), position(pair[1])
			if before >= 0 && after >= 0 {
				require.Greater(t, after, before, route.Path)
			}
		}
		encoded, err := json.Marshal(struct {
			Method, Path string
			Handlers     []string
		}{route.Method, route.Path, append([]string(nil), chain...)})
		require.NoError(t, err)
		t.Logf("S15_ROUTE_CHAIN %s", encoded)

	}
	sort.Strings(expected)
	sort.Strings(actual)
	require.Equal(t, expected, actual)
}

// 以下具名中间件仅标记 app 注入的安全边界；捕获链后提前中止，不执行用例。
func inventoryJWT(c *gin.Context)         { c.Next() }
func inventoryAdmin(c *gin.Context)       { c.Next() }
func inventoryAudit(c *gin.Context)       { c.Next() }
func inventoryStepUp(c *gin.Context)      { c.Next() }
func inventoryBackendAuth(c *gin.Context) { c.Next() }
func inventoryBackendUser(c *gin.Context) { c.Next() }
