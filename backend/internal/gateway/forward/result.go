package forward

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Result 表达本次转发观测，不持有旧实体、响应连接或具体平台。
type Result struct {
	RequestID                                  string
	UpstreamHeaders                            map[string][]string
	Usage                                      upstream.TokenUsage
	Model, UpstreamModel                       string
	Stream                                     bool
	Duration                                   time.Duration
	FirstTokenMs                               *int
	ClientDisconnect                           bool
	ReasoningEffort, RequestedReasoningEffort  *string
	UpstreamResponseServiceTier                string
	ServiceTier                                *string
	ImageCount                                 int
	ImageSize, ImageInputSize, ImageOutputSize string
	ImageOutputSizes                           []string
	ImageSizeSource                            string
	ImageSizeBreakdown                         map[string]int
	SearchCount                                int
	AudioUsage                                 *protocol.AudioUsage
}
