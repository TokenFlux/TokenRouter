package service

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

func decodeOpenAIJSONUseNumber(data []byte, dst any) error {
	return wirejson.DecodeUseNumber(data, dst)
}
