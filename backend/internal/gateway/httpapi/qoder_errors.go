// Qoder 三种协议的错误报文只由 HTTP Adapter 写出。
package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func WriteQoderStreamError(c *gin.Context, status int, errType, message string, streamStarted bool, endpoint QoderEndpoint) {
	if streamStarted || c.Writer.Written() {
		if !qoderRequestIsStream(c) {
			WriteQoderError(c, status, errType, message, endpoint)
			return
		}
		if endpoint == QoderResponses {
			if WriteResponsesFailedSSE(c, errType, "", message, ErrorRequestID(c), ErrorRequestModel(c)) {
				return
			}
		}
		if endpoint == QoderChat {
			WriteQoderChatErrorSSE(c, errType, message)
			return
		}
		errorEvent := `data: {"type":"error","error":{"type":` + strconv.Quote(errType) + `,"message":` + strconv.Quote(message) + `}}` + "\n\n"
		_, _ = c.Writer.WriteString(errorEvent)
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	WriteQoderError(c, status, errType, message, endpoint)
}
func WriteQoderChatErrorSSE(c *gin.Context, errType, message string) {
	errorEvent := `data: {"error":{"type":` + strconv.Quote(errType) + `,"message":` + strconv.Quote(message) + `}}` + "\n\n" + "data: [DONE]\n\n"
	_, _ = c.Writer.WriteString(errorEvent)
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
func WriteQoderError(c *gin.Context, status int, errType, message string, endpoint QoderEndpoint) {
	if endpoint == QoderMessages {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": message,
			},
		})
		return
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// 该快照由原请求观测时点设置，不读取或重解析 body。
func qoderRequestIsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, _ := c.Get("ops_stream")
	stream, _ := v.(bool)
	return stream
}
