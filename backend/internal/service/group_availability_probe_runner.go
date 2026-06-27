package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

const (
	groupAvailabilityProbeDefaultMaxWorkers  = 5
	groupAvailabilityProbeDefaultClaimLimit  = 20
	groupAvailabilityProbeLockDuration       = 5 * time.Minute
	groupAvailabilityProbeCleanupRetention   = 90 * 24 * time.Hour
	groupAvailabilityProbeCleanupMinInterval = 12 * time.Hour
)

// GroupAvailabilityProbeRunnerService 定期执行分组主动可用性探测。
type GroupAvailabilityProbeRunnerService struct {
	repo            GroupAvailabilityProbeRepository
	accountTestSvc  *AccountTestService
	gatewaySvc      *GatewayService
	openAIGateway   *OpenAIGatewayService
	geminiCompatSvc *GeminiMessagesCompatService
	cfg             *config.Config

	instanceID    string
	cron          *cron.Cron
	lastCleanupAt time.Time
	startOnce     sync.Once
	stopOnce      sync.Once
}

func NewGroupAvailabilityProbeRunnerService(
	repo GroupAvailabilityProbeRepository,
	accountTestSvc *AccountTestService,
	gatewaySvc *GatewayService,
	openAIGateway *OpenAIGatewayService,
	geminiCompatSvc *GeminiMessagesCompatService,
	cfg *config.Config,
) *GroupAvailabilityProbeRunnerService {
	return &GroupAvailabilityProbeRunnerService{
		repo:            repo,
		accountTestSvc:  accountTestSvc,
		gatewaySvc:      gatewaySvc,
		openAIGateway:   openAIGateway,
		geminiCompatSvc: geminiCompatSvc,
		cfg:             cfg,
		instanceID:      uuid.NewString(),
	}
}

func (s *GroupAvailabilityProbeRunnerService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.repo == nil || s.accountTestSvc == nil {
			logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] not started (missing dependencies)")
			return
		}

		loc := time.Local
		if s.cfg != nil {
			if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
				loc = parsed
			}
		}

		c := cron.New(cron.WithParser(scheduledTestCronParser), cron.WithLocation(loc))
		if _, err := c.AddFunc("* * * * *", func() { s.runDue() }); err != nil {
			logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] not started (invalid schedule): %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] started (tick=every minute)")
	})
}

func (s *GroupAvailabilityProbeRunnerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron == nil {
			return
		}
		ctx := s.cron.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
			logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] cron stop timed out")
		}
	})
}

func (s *GroupAvailabilityProbeRunnerService) runDue() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	now := time.Now()
	s.cleanupIfNeeded(ctx, now)

	dueGroups, err := s.repo.ClaimDue(ctx, now, now.Add(groupAvailabilityProbeLockDuration), s.instanceID, groupAvailabilityProbeDefaultClaimLimit)
	if err != nil {
		logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] ClaimDue error: %v", err)
		return
	}
	if len(dueGroups) == 0 {
		return
	}

	sem := make(chan struct{}, groupAvailabilityProbeDefaultMaxWorkers)
	var wg sync.WaitGroup
	for i := range dueGroups {
		group := dueGroups[i]
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOne(ctx, group)
		}()
	}
	wg.Wait()
}

func (s *GroupAvailabilityProbeRunnerService) cleanupIfNeeded(ctx context.Context, now time.Time) {
	if now.Sub(s.lastCleanupAt) < groupAvailabilityProbeCleanupMinInterval {
		return
	}
	s.lastCleanupAt = now
	if err := s.repo.CleanupOldResults(ctx, now.Add(-groupAvailabilityProbeCleanupRetention)); err != nil {
		logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] cleanup error: %v", err)
	}
}

func (s *GroupAvailabilityProbeRunnerService) runOne(ctx context.Context, due GroupAvailabilityProbeDueGroup) {
	config, err := normalizeGroupAvailabilityProbeConfig(due.Config)
	if err != nil {
		s.saveFailure(ctx, due, config, fmt.Sprintf("invalid probe config: %v", err), nil)
		return
	}
	if !config.Enabled {
		return
	}

	probeCtx := ctx
	cancel := func() {}
	if config.TimeoutSeconds > 0 {
		probeCtx, cancel = context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	}
	defer cancel()

	startedAt := time.Now()
	account, err := s.selectProbeAccount(probeCtx, due, config.ModelID)
	if err != nil {
		finishedAt := time.Now()
		s.saveResult(ctx, due, config, &GroupAvailabilityProbeResult{
			GroupID:      due.GroupID,
			ModelID:      config.ModelID,
			Status:       GroupAvailabilityProbeStatusFailed,
			Success:      false,
			LatencyMs:    finishedAt.Sub(startedAt).Milliseconds(),
			ErrorMessage: err.Error(),
			StartedAt:    startedAt,
			FinishedAt:   finishedAt,
		})
		return
	}

	result, err := s.accountTestSvc.RunTestBackgroundWithPromptAndUserAgent(probeCtx, account.ID, config.ModelID, config.Prompt, config.UserAgent)
	finishedAt := time.Now()
	probeResult := &GroupAvailabilityProbeResult{
		GroupID:    due.GroupID,
		AccountID:  &account.ID,
		ModelID:    config.ModelID,
		Status:     GroupAvailabilityProbeStatusSuccess,
		Success:    true,
		LatencyMs:  finishedAt.Sub(startedAt).Milliseconds(),
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	if result != nil {
		probeResult.Status = result.Status
		probeResult.Success = result.Status == GroupAvailabilityProbeStatusSuccess
		probeResult.LatencyMs = result.LatencyMs
		probeResult.ErrorMessage = result.ErrorMessage
		probeResult.StartedAt = result.StartedAt
		probeResult.FinishedAt = result.FinishedAt
	}
	if err != nil {
		probeResult.Status = GroupAvailabilityProbeStatusFailed
		probeResult.Success = false
		if probeResult.ErrorMessage == "" {
			probeResult.ErrorMessage = err.Error()
		}
	}
	if probeCtx.Err() != nil && probeResult.Success {
		probeResult.Status = GroupAvailabilityProbeStatusFailed
		probeResult.Success = false
		probeResult.ErrorMessage = probeCtx.Err().Error()
	}

	s.saveResult(ctx, due, config, probeResult)
}

func (s *GroupAvailabilityProbeRunnerService) saveFailure(ctx context.Context, due GroupAvailabilityProbeDueGroup, cfg GroupAvailabilityProbeConfig, message string, accountID *int64) {
	now := time.Now()
	modelID := strings.TrimSpace(cfg.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(due.Config.ModelID)
	}
	s.saveResult(ctx, due, cfg, &GroupAvailabilityProbeResult{
		GroupID:      due.GroupID,
		AccountID:    accountID,
		ModelID:      modelID,
		Status:       GroupAvailabilityProbeStatusFailed,
		Success:      false,
		ErrorMessage: message,
		StartedAt:    now,
		FinishedAt:   now,
	})
}

func (s *GroupAvailabilityProbeRunnerService) saveResult(ctx context.Context, due GroupAvailabilityProbeDueGroup, cfg GroupAvailabilityProbeConfig, result *GroupAvailabilityProbeResult) {
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Duration(defaultGroupAvailabilityProbeIntervalMinutes) * time.Minute
	}
	nextRunAt := time.Now().Add(interval)
	if err := s.repo.SaveResultAndScheduleNext(ctx, result, nextRunAt); err != nil {
		logger.LegacyPrintf("service.group_availability_probe", "[GroupAvailabilityProbe] group=%d save result error: %v", due.GroupID, err)
	}
}

func (s *GroupAvailabilityProbeRunnerService) selectProbeAccount(ctx context.Context, due GroupAvailabilityProbeDueGroup, modelID string) (*Account, error) {
	groupID := due.GroupID
	switch due.Platform {
	case PlatformOpenAI:
		if s.openAIGateway == nil {
			return nil, fmt.Errorf("openai gateway service not configured")
		}
		return s.openAIGateway.SelectAccountForModel(ctx, &groupID, "", modelID)
	case PlatformGemini:
		if s.geminiCompatSvc != nil {
			return s.geminiCompatSvc.SelectAccountForModel(ctx, &groupID, "", modelID)
		}
		if s.gatewaySvc != nil {
			return s.gatewaySvc.SelectAccountForModel(ctx, &groupID, "", modelID)
		}
		return nil, fmt.Errorf("gemini gateway service not configured")
	default:
		if s.gatewaySvc == nil {
			return nil, fmt.Errorf("gateway service not configured")
		}
		return s.gatewaySvc.SelectAccountForModel(ctx, &groupID, "", modelID)
	}
}
