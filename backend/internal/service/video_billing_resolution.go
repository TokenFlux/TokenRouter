package service

import (
	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

const VideoBillingResolution480P = purepricing.VideoBillingResolution480P
const VideoBillingResolution720P = purepricing.VideoBillingResolution720P
const VideoBillingResolution1080P = purepricing.VideoBillingResolution1080P

const VideoBillingMinDurationSeconds = purepricing.VideoBillingMinDurationSeconds
const VideoBillingMaxDurationSeconds = purepricing.VideoBillingMaxDurationSeconds
const VideoBillingDefaultDurationSeconds = purepricing.VideoBillingDefaultDurationSeconds

// NormalizeVideoBillingDurationSecondsOrDefault 委托唯一媒体定价规则。
func NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds int) int {
	return purepricing.NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
}

// LookupVideoBillingResolution 委托唯一媒体定价规则。
func LookupVideoBillingResolution(resolution string) (string, bool) {
	return purepricing.LookupVideoBillingResolution(resolution)
}

// NormalizeVideoBillingResolutionOrDefault 委托唯一媒体定价规则。
func NormalizeVideoBillingResolutionOrDefault(resolution string) string {
	return purepricing.NormalizeVideoBillingResolutionOrDefault(resolution)
}
