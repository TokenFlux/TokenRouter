package handler

import (
	"context"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

func usageRecordContextFromGin(c *gin.Context) context.Context {
	dst := context.Background()
	if c == nil || c.Request == nil {
		return dst
	}
	src := c.Request.Context()
	for _, key := range []any{
		ctxkey.RequestID,
		ctxkey.ClientRequestID,
		ctxkey.ClientModel,
	} {
		if value := src.Value(key); value != nil {
			dst = context.WithValue(dst, key, value)
		}
	}
	dst = service.PropagateAPIKeyModelRedirectTrace(dst, src)
	return dst
}

func wrapUsageRecordTaskContext(c *gin.Context, task func(context.Context)) func(context.Context) {
	if task == nil {
		return nil
	}
	requestCtx := usageRecordContextFromGin(c)
	return func(workerCtx context.Context) {
		base := workerCtx
		if base == nil {
			base = context.Background()
		}
		for _, key := range []any{
			ctxkey.RequestID,
			ctxkey.ClientRequestID,
			ctxkey.ClientModel,
		} {
			if value := requestCtx.Value(key); value != nil {
				base = context.WithValue(base, key, value)
			}
		}
		base = service.PropagateAPIKeyModelRedirectTrace(base, requestCtx)
		task(base)
	}
}
