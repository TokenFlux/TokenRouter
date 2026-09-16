package service

import (
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func countGrokNativeSearchCallsFromJSONBytes(body []byte) int {
	return nativegrok.CountGrokNativeSearchCallsFromJSONBytes(body)
}

func countGrokNativeSearchCallsFromSSEBody(body string) int {
	return nativegrok.CountGrokNativeSearchCallsFromSSEBody(body)
}

func countGrokNativeSearchCallsInSSEData(data []byte) int {
	return nativegrok.CountGrokNativeSearchCallsInSSEData(data)
}

func countGrokNativeSearchCallsInSSEDataDedup(data []byte, seen map[string]struct{}) int {
	return nativegrok.CountGrokNativeSearchCallsInSSEDataDedup(data, seen)
}
