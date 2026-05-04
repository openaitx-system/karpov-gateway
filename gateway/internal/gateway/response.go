package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response ���所有 REST 端点的标准响应结构。
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: 200, Message: "success", Data: data})
}

func Fail(c *gin.Context, httpStatus int, code int, message string) {
	c.AbortWithStatusJSON(httpStatus, Response{Code: code, Message: message})
}

func FailData(c *gin.Context, httpStatus int, code int, message string, data any) {
	c.AbortWithStatusJSON(httpStatus, Response{Code: code, Message: message, Data: data})
}

const (
	CodeUnauthorized   = 40100
	CodeForbidden      = 40300
	CodeNotFound       = 40400
	CodeBadRequest     = 40000
	CodeConflict       = 40900
	CodeRateLimited    = 42900
	CodeQuotaExceeded  = 42901
	CodeInternal       = 50000
	CodeCSRF           = 40301
)

// authUserID 从 gin context 安全取得当前认证用户 ID（中间件设置，不可伪造）。
func authUserID(c *gin.Context) string {
	return c.Request.Header.Get("X-User-Id")
}
