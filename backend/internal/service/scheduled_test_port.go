// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
)

type ScheduledTestPlan = account.ScheduledTestPlan

type ScheduledTestResult = account.ScheduledTestResult

type ScheduledTestPlanRepository = account.ScheduledTestPlanRepository

type ScheduledTestResultRepository = account.ScheduledTestResultRepository
