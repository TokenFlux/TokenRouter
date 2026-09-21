package httpapi

import (
	"errors"
	"fmt"
	"strings"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

	coderws "github.com/coder/websocket"
)

func SummarizeWSCloseErrorForLog(err error) (string, string) {
	if err == nil {
		return "-", "-"
	}
	statusCode := coderws.CloseStatus(err)
	if statusCode == -1 {
		return "-", "-"
	}
	closeStatus := fmt.Sprintf("%d(%s)", int(statusCode), statusCode.String())
	closeReason := "-"
	var closeErr coderws.CloseError
	if errors.As(err, &closeErr) {
		reason := strings.TrimSpace(closeErr.Reason)
		if reason != "" {
			closeReason = reason
		}
	}
	return closeStatus, closeReason
}

// ResponsesWSCloseInfo 只提取显式客户端关闭错误的状态和原因。
func ResponsesWSCloseInfo(err error) gatewayws.EntryClose {
	var closed *OpenAIWSClientCloseError
	if errors.As(err, &closed) {
		return gatewayws.EntryClose{Status: int(closed.StatusCode()), Reason: closed.Reason(), Present: true}
	}
	return gatewayws.EntryClose{}
}
