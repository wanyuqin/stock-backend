package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ─────────────────────────────────────────────────────────────────
// 统一响应结构
// 所有 API 均返回此格式，前端可统一处理。
//
// 成功：{"code":0,"message":"ok","data":{...}}
// 失败：{"code":非0,"message":"错误描述","data":null}
// ─────────────────────────────────────────────────────────────────

const (
	CodeOK            = 0
	CodeBadRequest    = 40000
	CodeNotFound      = 40400
	CodeInternalError = 50000
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// OK 返回 200 成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeOK,
		Message: "ok",
		Data:    data,
	})
}

// Fail 返回业务错误响应（HTTP 状态码与 code 分离）。
func Fail(c *gin.Context, httpStatus, bizCode int, msg string) {
	c.JSON(httpStatus, Response{
		Code:    bizCode,
		Message: msg,
		Data:    nil,
	})
}

// BadRequest 400
func BadRequest(c *gin.Context, msg string) {
	Fail(c, http.StatusBadRequest, CodeBadRequest, msg)
}

// NotFound 404
func NotFound(c *gin.Context, msg string) {
	Fail(c, http.StatusNotFound, CodeNotFound, msg)
}

// InternalError 500
func InternalError(c *gin.Context, msg string) {
	Fail(c, http.StatusInternalServerError, CodeInternalError, msg)
}
