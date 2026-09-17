package app

import backuphttp "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"

// 原路径测试复用所属 HTTP 适配器的门禁。
var requireCanonicalBackupID = backuphttp.RequireCanonicalBackupID
