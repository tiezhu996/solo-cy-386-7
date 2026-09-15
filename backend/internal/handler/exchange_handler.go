package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/marketpal/marketpal/internal/constants"
	"github.com/marketpal/marketpal/internal/dto"
	"github.com/marketpal/marketpal/internal/middleware"
	"github.com/marketpal/marketpal/internal/service"
	"github.com/marketpal/marketpal/internal/util"
)

// ExchangeHandler 换物提案 HTTP 处理器。
type ExchangeHandler struct {
	svc *service.ExchangeService
}

// NewExchangeHandler 构造换物提案处理器。
func NewExchangeHandler(svc *service.ExchangeService) *ExchangeHandler {
	return &ExchangeHandler{svc: svc}
}

// Create POST /api/v1/exchange/proposals
func (h *ExchangeHandler) Create(c *gin.Context) {
	var req dto.ExchangeCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "发起换物提案失败：参数校验不通过 "+err.Error())
		return
	}
	proposal, err := h.svc.Create(middleware.GetUserID(c), req)
	if err != nil {
		util.AbortWithError(c, err) // handler 再次包装由全局错误中间件统一输出
		return
	}
	util.OKMessage(c, constants.MsgExchangeCreated, service.ToExchangeProposalVO(proposal))
}

// Accept POST /api/v1/exchange/proposals/:id/accept
func (h *ExchangeHandler) Accept(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "接受换物提案失败：提案 id 参数非法")
		return
	}
	proposal, err := h.svc.Accept(middleware.GetUserID(c), uint(id))
	if err != nil {
		util.AbortWithError(c, err)
		return
	}
	util.OKMessage(c, constants.MsgExchangeAccepted, service.ToExchangeProposalVO(proposal))
}

// Reject POST /api/v1/exchange/proposals/:id/reject
func (h *ExchangeHandler) Reject(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "拒绝换物提案失败：提案 id 参数非法")
		return
	}
	var req dto.ExchangeRejectRequest
	_ = c.ShouldBindJSON(&req)
	proposal, err := h.svc.Reject(middleware.GetUserID(c), uint(id), req.Note)
	if err != nil {
		util.AbortWithError(c, err)
		return
	}
	util.OKMessage(c, constants.MsgExchangeRejected, service.ToExchangeProposalVO(proposal))
}

// Counter POST /api/v1/exchange/proposals/:id/counter
func (h *ExchangeHandler) Counter(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "还价失败：提案 id 参数非法")
		return
	}
	var req dto.ExchangeCounterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "还价失败：参数校验不通过 "+err.Error())
		return
	}
	proposal, err := h.svc.Counter(middleware.GetUserID(c), uint(id), req)
	if err != nil {
		util.AbortWithError(c, err)
		return
	}
	util.OKMessage(c, constants.MsgExchangeCountered, service.ToExchangeProposalVO(proposal))
}

// Cancel POST /api/v1/exchange/proposals/:id/cancel
func (h *ExchangeHandler) Cancel(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "取消换物提案失败：提案 id 参数非法")
		return
	}
	var req dto.ExchangeRejectRequest
	_ = c.ShouldBindJSON(&req)
	proposal, err := h.svc.Cancel(middleware.GetUserID(c), uint(id), req.Note)
	if err != nil {
		util.AbortWithError(c, err)
		return
	}
	util.OKMessage(c, constants.MsgExchangeCancelled, service.ToExchangeProposalVO(proposal))
}

// List GET /api/v1/exchange/proposals
func (h *ExchangeHandler) List(c *gin.Context) {
	var q dto.ExchangeQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "换物提案列表失败：查询参数不合法 "+err.Error())
		return
	}
	res, err := h.svc.List(middleware.GetUserID(c), q)
	if err != nil {
		util.AbortWithError(c, err)
		return
	}
	util.OK(c, res)
}

// Detail GET /api/v1/exchange/proposals/:id
func (h *ExchangeHandler) Detail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "换物提案详情失败：提案 id 参数非法")
		return
	}
	proposal, err := h.svc.GetDetail(middleware.GetUserID(c), uint(id))
	if err != nil {
		util.AbortWithError(c, err)
		return
	}
	util.OK(c, service.ToExchangeProposalVO(proposal))
}
