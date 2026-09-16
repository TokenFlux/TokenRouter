package service

import completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"

func optionalTrimmedStringPtr(raw string) *string {
	return completion.OptionalTrimmedStringPtr(raw)
}
