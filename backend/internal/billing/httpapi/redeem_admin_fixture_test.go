package httpapi

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/gin-gonic/gin"
)

// redeemAdminFixture 只提供原兑换 HTTP 断言需要的用例及调用记录。
type redeemAdminFixture struct {
	redeems             []billing.RedeemCode
	lastListRedeemCodes struct {
		codeType, status, search, sortBy, sortOrder string
		calls                                       int
	}
	lastGenerateRedeemCodes *billing.GenerateRedeemCodesInput
}

func newRedeemAdminFixture() *redeemAdminFixture {
	return &redeemAdminFixture{redeems: []billing.RedeemCode{{ID: 5, Code: "R-TEST", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusUnused, CreatedAt: time.Now().UTC()}}}
}

// setupRedeemAdminContractRouter 直接装配原生处理器，保留原端点覆盖。
func setupRedeemAdminContractRouter() (*gin.Engine, *redeemAdminFixture) {
	router := gin.New()
	source := newRedeemAdminFixture()
	handler := NewAdminRedeemHandler(source, nil)
	router.GET("/api/v1/admin/redeem-codes", handler.List)
	router.GET("/api/v1/admin/redeem-codes/:id", handler.GetByID)
	router.POST("/api/v1/admin/redeem-codes", handler.Generate)
	router.PUT("/api/v1/admin/redeem-codes/:id", handler.Update)
	router.DELETE("/api/v1/admin/redeem-codes/:id", handler.Delete)
	router.POST("/api/v1/admin/redeem-codes/batch-delete", handler.BatchDelete)
	router.POST("/api/v1/admin/redeem-codes/:id/expire", handler.Expire)
	router.GET("/api/v1/admin/redeem-codes/:id/stats", handler.GetStats)
	return router, source
}

func (s *redeemAdminFixture) ListRedeemCodes(ctx context.Context, page, pageSize int, codeType, status, search string, sortBy, sortOrder string) ([]billing.RedeemCode, int64, error) {
	s.lastListRedeemCodes.codeType = codeType
	s.lastListRedeemCodes.status = status
	s.lastListRedeemCodes.search = search
	s.lastListRedeemCodes.sortBy = sortBy
	s.lastListRedeemCodes.sortOrder = sortOrder
	s.lastListRedeemCodes.calls++
	return s.redeems, int64(len(s.redeems)), nil
}

func (s *redeemAdminFixture) GetRedeemCode(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	code := billing.RedeemCode{ID: id, Code: "R-TEST", Status: billing.StatusUnused}
	return &code, nil
}

func (s *redeemAdminFixture) GenerateRedeemCodes(ctx context.Context, input *billing.GenerateRedeemCodesInput) ([]billing.RedeemCode, error) {
	s.lastGenerateRedeemCodes = input
	return s.redeems, nil
}

func (s *redeemAdminFixture) UpdateRedeemCode(ctx context.Context, id int64, input *billing.UpdateRedeemCodeInput) (*billing.RedeemCode, error) {
	code := billing.RedeemCode{ID: id, Code: "R-TEST", Status: billing.StatusUnused, MaxUses: 1}
	if input.MaxUses != nil {
		code.MaxUses = *input.MaxUses
	}
	if input.Value != nil {
		code.Value = *input.Value
	}
	if input.ExpiresAtSet {
		code.ExpiresAt = input.ExpiresAt
	}
	if input.PlanID != nil {
		code.Type = billing.RedeemTypeSubscription
		code.PlanID = input.PlanID
	}
	return &code, nil
}

func (s *redeemAdminFixture) DeleteRedeemCode(ctx context.Context, id int64) error {
	return nil
}

func (s *redeemAdminFixture) BatchDeleteRedeemCodes(ctx context.Context, ids []int64) (int64, error) {
	return int64(len(ids)), nil
}

func (s *redeemAdminFixture) ExpireRedeemCode(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	code := billing.RedeemCode{ID: id, Code: "R-TEST", Status: billing.StatusUsed}
	return &code, nil
}
