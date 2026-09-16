// 风险通知只携带已确定的处置与展示数据，禁止携带正文、媒体或审核凭据。
package contract

import "time"

type RiskPolicy struct{ BanThreshold, CyberBanThreshold int }
type RiskLog struct {
	ID                                    int64
	UserID                                *int64
	UserEmail, GroupName, HighestCategory string
	HighestScore                          float64
	ViolationCount                        int
	AutoBanned                            bool
	CreatedAt                             time.Time
}
type RiskWarning struct {
	ID                                int64
	UserID                            *int64
	UserEmail, GroupName, AccountName string
	ViolationCount                    int
	CreatedAt                         time.Time
}
