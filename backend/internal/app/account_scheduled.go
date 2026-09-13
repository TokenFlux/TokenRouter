// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	time "time"
)

// provideScheduledTests 固定唯一计划/结果用例，构造无定时器或后台任务。
func provideScheduledTests(plans account.ScheduledTestPlanRepository, results account.ScheduledTestResultRepository) *account.ScheduledTestService {
	return account.NewScheduledTestService(plans, results, account.ScheduledTestOptions{Now: time.Now, NextRun: accountprovider.NextScheduledTestRun})
}

func provideScheduledTestRunner(plans account.ScheduledTestPlanRepository, scheduled *account.ScheduledTestService, tests *account.TestService, recovery *account.RecoveryService, cfg *config.Config) *account.ScheduledTestRunnerService {
	location := time.Local
	if parsed, err := time.LoadLocation(cfg.Timezone); err == nil && parsed != nil {
		location = parsed
	}
	return account.NewScheduledTestRunnerService(plans, scheduled, tests, account.ScheduledRunnerOptions{Schedule: accountprovider.NewScheduledCron(location), Now: time.Now, NextRun: accountprovider.NextScheduledTestRun, Offset: 10 * time.Second, Observe: func(format string, args ...any) {
		logging.LegacyPrintf("service.scheduled_test_runner", format, args...)
	}, Recover: recovery.RecoverAccountAfterSuccessfulTest})
}
