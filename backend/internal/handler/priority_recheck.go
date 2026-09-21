package handler

import (
	"net/http"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/middleware"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/service"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/util"
	"github.com/gin-gonic/gin"
)

type PriorityRecheckHandler struct {
	service service.PriorityRecheckService
}

func NewPriorityRecheckHandler(s service.PriorityRecheckService) *PriorityRecheckHandler {
	return &PriorityRecheckHandler{service: s}
}

func (h *PriorityRecheckHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/rechecks")
	resource.GET("", h.list)
	resource.GET("/:id", h.get)
	resource.POST("/:id/resolve", middleware.RequireMinimumRole(model.RoleReviewer), h.resolve)
}

func (h *PriorityRecheckHandler) list(c *gin.Context) {
	var query dto.RecheckQuery
	_ = c.ShouldBindQuery(&query)
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 || query.PageSize > 100 {
		query.PageSize = 20
	}
	result, err := h.service.List(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}

func (h *PriorityRecheckHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *PriorityRecheckHandler) resolve(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.ResolvePriorityRecheck
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Resolve(c.Request.Context(), id, input, actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}
