package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

func OptionsFromConfig(cfg config.LogConfig) logging.InitOptions {
	return logging.InitOptions{
		Level:           cfg.Level,
		Format:          cfg.Format,
		ServiceName:     cfg.ServiceName,
		Environment:     cfg.Environment,
		Caller:          cfg.Caller,
		StacktraceLevel: cfg.StacktraceLevel,
		Output: logging.OutputOptions{
			ToStdout: cfg.Output.ToStdout,
			ToFile:   cfg.Output.ToFile,
			FilePath: cfg.Output.FilePath,
		},
		Rotation: logging.RotationOptions{
			MaxSizeMB:  cfg.Rotation.MaxSizeMB,
			MaxBackups: cfg.Rotation.MaxBackups,
			MaxAgeDays: cfg.Rotation.MaxAgeDays,
			Compress:   cfg.Rotation.Compress,
			LocalTime:  cfg.Rotation.LocalTime,
		},
		Sampling: logging.SamplingOptions{
			Enabled:    cfg.Sampling.Enabled,
			Initial:    cfg.Sampling.Initial,
			Thereafter: cfg.Sampling.Thereafter,
		},
	}
}
