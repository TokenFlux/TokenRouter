package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// logging.go converts process logging configuration into initialization options
// for sinks, rotation, and sampling. OptionsFromConfig copies those settings
// into logging.InitOptions, including megabyte and day limits, so
// initialization receives the values selected during configuration loading.

// OptionsFromConfig converts process log configuration into logger initialization options.
func OptionsFromConfig(cfg config.LogConfig, environment string) logging.InitOptions {
	return logging.InitOptions{
		Level:           cfg.Level,
		Format:          cfg.Format,
		ServiceName:     cfg.ServiceName,
		Environment:     environment,
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
