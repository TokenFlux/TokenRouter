// LogControl 只操作已有唯一日志后端，不持有第二份状态。
package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"go.uber.org/zap"
)

type LogControl struct{}

func (LogControl) Apply(cfg *ops.OpsRuntimeLogConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil runtime log config")
	}
	if err := logging.Reconfigure(func(opts *logging.InitOptions) error {
		opts.Level = strings.ToLower(strings.TrimSpace(cfg.Level))
		opts.Caller = cfg.Caller
		opts.StacktraceLevel = strings.ToLower(strings.TrimSpace(cfg.StacktraceLevel))
		opts.Sampling.Enabled = cfg.EnableSampling
		opts.Sampling.Initial = cfg.SamplingInitial
		opts.Sampling.Thereafter = cfg.SamplingNext
		return nil
	}); err != nil {
		return err
	}
	return nil
}
func (LogControl) Changed(operatorID int64, oldCfg *ops.OpsRuntimeLogConfig, newCfg *ops.OpsRuntimeLogConfig, action string) {
	oldRaw, _ := json.Marshal(oldCfg)
	newRaw, _ := json.Marshal(newCfg)
	logging.With(
		zap.String("component", "audit.log_config_change"),
		zap.String("action", strings.TrimSpace(action)),
		zap.Int64("operator_id", operatorID),
		zap.String("old", string(oldRaw)),
		zap.String("new", string(newRaw)),
	).Info("runtime log config changed")
}
func (LogControl) Failed(operatorID int64, oldCfg *ops.OpsRuntimeLogConfig, newCfg *ops.OpsRuntimeLogConfig, reason string) {
	oldRaw, _ := json.Marshal(oldCfg)
	newRaw, _ := json.Marshal(newCfg)
	logging.With(
		zap.String("component", "audit.log_config_change"),
		zap.String("action", "failed"),
		zap.Int64("operator_id", operatorID),
		zap.String("reason", strings.TrimSpace(reason)),
		zap.String("old", string(oldRaw)),
		zap.String("new", string(newRaw)),
	).Warn("runtime log config change failed")
}
