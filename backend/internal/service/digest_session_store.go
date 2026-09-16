// 摘要会话只保留一个缓存，旧构造器委托 gateway/session。
package service

import "github.com/TokenFlux/TokenRouter/internal/gateway/session"

type DigestSessionStore = session.DigestSessionStore

func NewDigestSessionStore() *DigestSessionStore { return session.NewDigestSessionStore() }
