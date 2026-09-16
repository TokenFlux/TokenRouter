// 旧平台入口只创建无状态 codec；通用协议实现、模型目录和缓存仍各有唯一来源。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/google/uuid"
)

func grokBodyCodec() grok.BodyCodec { return grok.BodyCodec{NewID: uuid.NewString} }
