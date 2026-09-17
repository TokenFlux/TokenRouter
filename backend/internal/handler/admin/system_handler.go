package admin

import (
	oh "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
)

type SystemHandler = oh.SystemHandler
type RestartRequester = maintenance.RestartRequester
type systemUpdateService = maintenance.UpdateAPI

func NewSystemHandler(update systemUpdateService, lock *maintenance.SystemOperationLockService, restart ...RestartRequester) *SystemHandler {
	return oh.NewSystemHandler(update, lock, restart...)
}
