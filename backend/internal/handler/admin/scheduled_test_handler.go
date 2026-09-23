package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ScheduledTestHandler handles admin scheduled-test-plan management.
type ScheduledTestHandler struct {
	scheduledTestSvc *service.ScheduledTestService
}

// NewScheduledTestHandler creates a new ScheduledTestHandler.
func NewScheduledTestHandler(scheduledTestSvc *service.ScheduledTestService) *ScheduledTestHandler {
	return &ScheduledTestHandler{scheduledTestSvc: scheduledTestSvc}
}

type createScheduledTestPlanRequest struct {
	AccountID      int64   `json:"account_id" binding:"required"`
	ModelID        string  `json:"model_id"`
	CronExpression string  `json:"cron_expression" binding:"required"`
	Enabled        *bool   `json:"enabled"`
	MaxResults     int     `json:"max_results"`
	AutoRecover    *bool   `json:"auto_recover"`
	TestKind       string  `json:"test_kind"`
	QuestionIDs    []int64 `json:"question_ids"`
	CustomPrompt   string  `json:"custom_prompt"`
}

type updateScheduledTestPlanRequest struct {
	ModelID        string   `json:"model_id"`
	CronExpression string   `json:"cron_expression"`
	Enabled        *bool    `json:"enabled"`
	MaxResults     int      `json:"max_results"`
	AutoRecover    *bool    `json:"auto_recover"`
	QuestionIDs    *[]int64 `json:"question_ids"`
	CustomPrompt   *string  `json:"custom_prompt"`
}

// ListByAccount GET /admin/accounts/:id/scheduled-test-plans
func (h *ScheduledTestHandler) ListByAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid account id")
		return
	}

	plans, err := h.scheduledTestSvc.ListPlansByAccount(c.Request.Context(), accountID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

// Create POST /admin/scheduled-test-plans
func (h *ScheduledTestHandler) Create(c *gin.Context) {
	var req createScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	plan := &service.ScheduledTestPlan{
		AccountID:      req.AccountID,
		ModelID:        req.ModelID,
		CronExpression: req.CronExpression,
		Enabled:        true,
		MaxResults:     req.MaxResults,
		TestKind:       req.TestKind,
		QuestionIDs:    req.QuestionIDs,
		CustomPrompt:   req.CustomPrompt,
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}

	created, err := h.scheduledTestSvc.CreatePlan(c.Request.Context(), plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}

// Update PUT /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Update(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}

	var req updateScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.ModelID != "" {
		existing.ModelID = req.ModelID
	}
	if req.CronExpression != "" {
		existing.CronExpression = req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.MaxResults > 0 {
		existing.MaxResults = req.MaxResults
	}
	if req.AutoRecover != nil {
		existing.AutoRecover = *req.AutoRecover
	}
	if req.QuestionIDs != nil {
		existing.QuestionIDs = *req.QuestionIDs
	}
	if req.CustomPrompt != nil {
		existing.CustomPrompt = *req.CustomPrompt
	}

	updated, err := h.scheduledTestSvc.UpdatePlan(c.Request.Context(), existing)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete DELETE /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Delete(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	if err := h.scheduledTestSvc.DeletePlan(c.Request.Context(), planID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListResults GET /admin/scheduled-test-plans/:id/results
func (h *ScheduledTestHandler) ListResults(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = min(l, 200)
	}

	results, err := h.scheduledTestSvc.ListResults(c.Request.Context(), planID, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}

func (h *ScheduledTestHandler) ListQuestions(c *gin.Context) {
	questions, err := h.scheduledTestSvc.ListQuestions(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, questions)
}

func (h *ScheduledTestHandler) SaveQuestion(c *gin.Context) {
	var question service.IntelligenceQuestion
	if err := c.ShouldBindJSON(&question); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if id := c.Param("id"); id != "" {
		parsed, err := strconv.ParseInt(id, 10, 64)
		if err != nil || parsed < 1 {
			response.BadRequest(c, "invalid question id")
			return
		}
		question.ID = parsed
	}
	question.BuiltIn = false
	saved, err := h.scheduledTestSvc.SaveQuestion(c.Request.Context(), &question)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (h *ScheduledTestHandler) DeleteQuestion(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid question id")
		return
	}
	if err := h.scheduledTestSvc.DeleteQuestion(c.Request.Context(), id); err != nil {
		response.BadRequest(c, "question is in use or cannot be deleted")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ScheduledTestHandler) RunNow(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid plan id")
		return
	}
	plan, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}
	if plan.TestKind != "intelligence" {
		response.BadRequest(c, "not an intelligence plan")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	result, err := h.scheduledTestSvc.RunIntelligenceNow(ctx, plan)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ScheduledTestHandler) Review(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID < 1 {
		response.BadRequest(c, "invalid plan id")
		return
	}
	resultID, err := strconv.ParseInt(c.Param("result_id"), 10, 64)
	if err != nil || resultID < 1 {
		response.BadRequest(c, "invalid result id")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.BadRequest(c, "missing reviewer")
		return
	}
	var req struct {
		Score *int   `json:"score" binding:"required"`
		Note  string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Score == nil {
		response.BadRequest(c, "score required")
		return
	}
	result, err := h.scheduledTestSvc.Review(c.Request.Context(), planID, resultID, subject.UserID, *req.Score, req.Note)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}
