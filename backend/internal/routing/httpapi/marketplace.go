package httpapi

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// marketplace.go serves public and personal model listings with homepage
// statistics.
// Public model browsing can omit current capacity aggregation while retaining
// model, price, and availability data returned by routing. The HTTP parameter
// selects that work before the marketplace query; homepage totals use a
// separate statistics reader and the same success envelope. Personal browsing
// reads current grants for the authenticated subject, and private responses
// instruct shared and browser HTTP caches not to store their contents.

// MarketplaceStatsReader supplies token and user totals for public homepage
// responses.
type MarketplaceStatsReader interface {
	// PublicStats returns the current public token and user counters for the
	// supplied request context. Reader failures become the endpoint's standard
	// error response, so callers return the error with the failed read.
	PublicStats(context.Context) (dto.ModelMarketplaceStats, error)
}

// MarketplaceGroupAccess reads current exclusive grants for authenticated users.
type MarketplaceGroupAccess interface {
	// AllowedGroupIDs returns current exclusive grants for the supplied account.
	// A read failure must return an error rather than an empty permission set;
	// callers must authenticate userID before invoking this method.
	AllowedGroupIDs(context.Context, int64) ([]int64, error)
}

// MarketplaceHandler serves public and personal browsing with aggregate homepage
// statistics.
type MarketplaceHandler struct {
	marketplace *routing.Marketplace   // authorized group model reader
	stats       MarketplaceStatsReader // public homepage totals reader
	access      MarketplaceGroupAccess // current exclusive grant reader
}

// NewMarketplaceHandler binds marketplace data, public statistics, and user grants.
// The access reader is required for personal queries; a nil reader leaves public
// queries usable and makes personal queries fail closed.
func NewMarketplaceHandler(marketplace *routing.Marketplace, stats MarketplaceStatsReader, access MarketplaceGroupAccess) *MarketplaceHandler {
	return &MarketplaceHandler{marketplace: marketplace, stats: stats, access: access}
}

// ListPublic returns publicly visible model groups and their display data.
// include_capacity defaults to true; false skips capacity aggregation, and an
// invalid boolean returns INVALID_INCLUDE_CAPACITY before reading marketplace
// groups.
func (h *MarketplaceHandler) ListPublic(c *gin.Context) {
	includeCapacity, err := strconv.ParseBool(c.DefaultQuery("include_capacity", "true"))
	if err != nil {
		httpx.ErrorFrom(c, apperror.BadRequest("INVALID_INCLUDE_CAPACITY", "include_capacity must be a boolean"))
		return
	}
	groups, err := h.marketplace.ListPublic(c.Request.Context(), routing.MarketplaceListOptions{IncludeCapacity: includeCapacity})
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, dto.ModelMarketplaceGroupsFromRouting(groups))
}

// ListAvailable returns public groups and the authenticated user's exclusive groups.
// User identity comes only from JWT middleware; query parameters cannot select
// another account or grant access. Permission failures stop catalog assembly,
// and include_capacity has the same default and validation as public browsing.
func (h *MarketplaceHandler) ListAvailable(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		httpx.Unauthorized(c, "User not authenticated")
		return
	}
	includeCapacity, err := strconv.ParseBool(c.DefaultQuery("include_capacity", "true"))
	if err != nil {
		httpx.ErrorFrom(c, apperror.BadRequest("INVALID_INCLUDE_CAPACITY", "include_capacity must be a boolean"))
		return
	}
	if h.access == nil {
		httpx.ErrorFrom(c, apperror.InternalServer("MARKETPLACE_ACCESS_UNAVAILABLE", "Marketplace permissions are unavailable"))
		return
	}
	allowed, err := h.access.AllowedGroupIDs(c.Request.Context(), subject.UserID)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	groups, err := h.marketplace.ListVisible(c.Request.Context(), allowed, routing.MarketplaceListOptions{IncludeCapacity: includeCapacity})
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, dto.ModelMarketplaceGroupsFromRouting(groups))
}

// StatsPublic returns homepage token and user counts through the configured
// reader. It passes the HTTP request context to that reader and exposes read
// failures through the standard error response.
func (h *MarketplaceHandler) StatsPublic(c *gin.Context) {
	stats, err := h.stats.PublicStats(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	httpx.Success(c, stats)
}

// RegisterPublicMarketplaceRoutes binds model browsing and homepage totals
// under /marketplace. The caller supplies the router group used for public
// access; this registration adds the two GET handlers directly.
func RegisterPublicMarketplaceRoutes(v1 *gin.RouterGroup, endpoint *MarketplaceHandler) {
	marketplace := v1.Group("/marketplace")
	marketplace.GET("/models", endpoint.ListPublic)
	marketplace.GET("/stats", endpoint.StatsPublic)
}

// RegisterUserMarketplaceRoutes binds personal browsing below authenticated routes.
// The caller installs JWT, panel limits, and audit middleware before registration;
// public marketplace registration remains separate and never consults user grants.
func RegisterUserMarketplaceRoutes(authenticated *gin.RouterGroup, endpoint *MarketplaceHandler) {
	authenticated.GET("/marketplace/models/available", endpoint.ListAvailable)
}
