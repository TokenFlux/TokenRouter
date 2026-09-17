package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/backup"
	bh "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
)

type DataManagementHandler = bh.DataManagementHandler

func NewDataManagementHandler(s *backup.DataManagementService) *DataManagementHandler {
	return bh.NewDataManagementHandler(s)
}
