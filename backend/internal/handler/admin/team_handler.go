package admin

import (
	"strconv"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// TeamHandler 处理平台管理员的团队运维操作。
type TeamHandler struct {
	service *service.TeamService
}

func NewTeamHandler(teamService *service.TeamService) *TeamHandler {
	return &TeamHandler{service: teamService}
}

func (h *TeamHandler) List(c *gin.Context) {
	items, err := h.service.AdminList(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *TeamHandler) Get(c *gin.Context) {
	teamID, ok := adminTeamID(c)
	if !ok {
		return
	}
	teamCtx, err := h.service.AdminGet(c.Request.Context(), teamID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, teamCtx)
}

func (h *TeamHandler) ListMembers(c *gin.Context) {
	teamID, ok := adminTeamID(c)
	if !ok {
		return
	}
	items, err := h.service.AdminListMembers(c.Request.Context(), teamID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *TeamHandler) GetUsage(c *gin.Context) {
	teamID, ok := adminTeamID(c)
	if !ok {
		return
	}
	query := service.TeamUsageQuery{}
	// 管理员统计弹窗按成员并行查询同一团队，空值表示团队整体。
	if rawMemberID := strings.TrimSpace(c.Query("member_id")); rawMemberID != "" {
		memberID, err := strconv.ParseInt(rawMemberID, 10, 64)
		if err != nil || memberID <= 0 {
			response.BadRequest(c, "Invalid member_id")
			return
		}
		query.ActorUserID = &memberID
	}
	item, err := h.service.AdminGetUsageSummary(c.Request.Context(), teamID, query)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *TeamHandler) Create(c *gin.Context) {
	var req struct {
		OwnerUserID int64  `json:"owner_user_id" binding:"required"`
		Name        string `json:"name" binding:"required"`
		MemberLimit *int   `json:"member_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	teamCtx, err := h.service.AdminCreate(c.Request.Context(), req.OwnerUserID, req.Name, req.MemberLimit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, teamCtx)
}

func (h *TeamHandler) Update(c *gin.Context) {
	teamID, ok := adminTeamID(c)
	if !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Status      *string `json:"status"`
		MemberLimit *int    `json:"member_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	teamCtx, err := h.service.AdminUpdate(c.Request.Context(), teamID, service.TeamAdminUpdate{
		Name: req.Name, Status: req.Status, MemberLimit: req.MemberLimit,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, teamCtx)
}

func (h *TeamHandler) ForceTransfer(c *gin.Context) {
	teamID, ok := adminTeamID(c)
	if !ok {
		return
	}
	var req struct {
		TargetUserID int64 `json:"target_user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	teamCtx, err := h.service.AdminForceTransfer(c.Request.Context(), teamID, req.TargetUserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, teamCtx)
}

func (h *TeamHandler) Dissolve(c *gin.Context) {
	teamID, ok := adminTeamID(c)
	if !ok {
		return
	}
	if err := h.service.AdminDissolve(c.Request.Context(), teamID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"dissolved": true})
}

func adminTeamID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid team ID")
		return 0, false
	}
	return id, true
}
