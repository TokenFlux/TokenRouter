package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// 临时停调值与缓存契约由账号模块唯一拥有。
type TempUnschedState = accountcore.TempUnschedState
type TempUnschedCache = accountcore.TempUnschedCache

type OpenAIAPIKeyHealthCache = accountcore.OpenAIAPIKeyHealthCache

type TimeoutCounterCache = accountcore.TimeoutCounterCache
