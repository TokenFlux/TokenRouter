// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

type TLSFingerprintCollectorStatus = egress.TLSFingerprintCollectorStatus
type TLSFingerprintCollectorSession = egress.TLSFingerprintCollectorSession
type TLSFingerprintCaptureRecord = egress.TLSFingerprintCaptureRecord
type TLSFingerprintCollectorService = provider.TLSFingerprintCollectorService

func NewTLSFingerprintCollectorService(cfg *config.Config) *TLSFingerprintCollectorService {
	options := provider.CollectorOptions{Diagnostics: egress.Diagnostics{Logf: logger.LegacyPrintf}}
	if cfg != nil {
		v := cfg.Server.TLSFingerprintCollector
		options.Host = v.Host
		options.Port = v.Port
		options.PublicBaseURL = v.PublicBaseURL
		options.CertFile = v.CertFile
		options.KeyFile = v.KeyFile
		options.SessionTTLSeconds = v.SessionTTLSeconds
		options.MaxRecordsPerSession = v.MaxRecordsPerSession
	}
	return provider.NewTLSFingerprintCollectorService(options)
}
