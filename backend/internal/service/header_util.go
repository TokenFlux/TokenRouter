// 旧 Header 入口只委托原平台 wire 策略，后续网关阶段清理。
package service

import (
	"net/http"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

func resolveWireCasing(key string) string            { return native.ResolveWireCasing(key) }
func setHeaderRaw(h http.Header, key, value string)  { native.SetHeaderRaw(h, key, value) }
func addHeaderRaw(h http.Header, key, value string)  { native.AddHeaderRaw(h, key, value) }
func deleteHeaderAllForms(h http.Header, key string) { native.DeleteHeaderAllForms(h, key) }
func getHeaderRaw(h http.Header, key string) string  { return native.GetHeaderRaw(h, key) }
func sortHeadersByWireOrder(h http.Header) []string  { return native.SortHeadersByWireOrder(h) }
