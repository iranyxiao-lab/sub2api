package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *ScheduledTestHandler) intelligencePlan(c *gin.Context) *service.ScheduledTestPlan {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid plan id")
		return nil
	}
	plan, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), id)
	if err != nil || plan == nil || plan.TestKind != "intelligence" {
		response.NotFound(c, "intelligence plan not found")
		return nil
	}
	return plan
}

func (h *ScheduledTestHandler) CreateRun(c *gin.Context) {
	plan := h.intelligencePlan(c)
	if plan == nil {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if _, err := uuid.Parse(key); err != nil {
		response.BadRequest(c, "Idempotency-Key must be a UUID")
		return
	}
	run, err := h.scheduledTestSvc.EnqueueRun(c.Request.Context(), plan, key, "manual")
	if errors.Is(err, service.ErrIntelligenceRunActive) {
		c.JSON(http.StatusConflict, gin.H{"message": "A test is already running"})
		return
	}
	if err != nil {
		response.InternalError(c, "Unable to enqueue intelligence test")
		return
	}
	c.JSON(http.StatusAccepted, run)
}

func (h *ScheduledTestHandler) ListRuns(c *gin.Context) {
	plan := h.intelligencePlan(c)
	if plan == nil {
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		response.BadRequest(c, "invalid page")
		return
	}
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if err != nil {
		response.BadRequest(c, "invalid page size")
		return
	}
	status := c.Query("status")
	if page < 1 || page > 100000 || size < 1 || size > 20 {
		response.BadRequest(c, "invalid pagination")
		return
	}
	switch status {
	case "", "queued", "running", "success", "failed", "interrupted":
	default:
		response.BadRequest(c, "invalid status")
		return
	}
	runs, err := h.scheduledTestSvc.ListRuns(c.Request.Context(), plan.ID, status, page, size)
	if err != nil {
		response.InternalError(c, "Unable to load intelligence history")
		return
	}
	c.JSON(http.StatusOK, runs)
}

func (h *ScheduledTestHandler) GetRun(c *gin.Context) {
	plan := h.intelligencePlan(c)
	if plan == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("run_id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid run id")
		return
	}
	run, err := h.scheduledTestSvc.GetRun(c.Request.Context(), plan.ID, id)
	if err != nil {
		response.NotFound(c, "run not found")
		return
	}
	c.JSON(http.StatusOK, run)
}
