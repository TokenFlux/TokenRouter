//go:build unit

// 原私有测试入口仅作委托；生产消费者清零后不保留普通构建符号。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/usageprovider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/zhipu"
	"github.com/tidwall/gjson"
)

func cnMillisToRFC3339(n int64) string                     { return native.CnMillisToRFC3339(n) }
func cnNormalizeResetTime(raw any) string                  { return native.CnNormalizeResetTime(raw) }
func cnParseF64(raw any) (float64, bool)                   { return native.CnParseF64(raw) }
func parseZhipuTokenTiers(data gjson.Result) []CNQuotaTier { return zhipu.ParseZhipuTokenTiers(data) }

func parseKimiUsageTiers(body []byte) []CNQuotaTier { return kimi.ParseKimiUsageTiers(body) }
