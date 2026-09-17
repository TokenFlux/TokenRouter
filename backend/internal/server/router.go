package server

import "github.com/gin-gonic/gin"

// RouterRuntime 只接收 app 已装配的 HTTP 行为；不持有业务实例。
type RouterRuntime struct {
	Middleware []gin.HandlerFunc
	Frontend   gin.HandlerFunc
	Register   []func(*gin.Engine)
}

// SetupRouter 保持全局中间件、静态资源及路由挂载的原顺序。
func SetupRouter(r *gin.Engine, runtime *RouterRuntime) *gin.Engine {
	r.Use(runtime.Middleware...)
	if runtime.Frontend != nil {
		r.Use(runtime.Frontend)
	}
	RegisterCommonRoutes(r)
	for _, register := range runtime.Register {
		register(r)
	}
	return r
}
