// Package httpx 统一 API 的 JSON 响应体与错误体，保证前后端契约一致（§3.3）。
package httpx

import (
	"encoding/json"
	"net/http"
)

// 错误码，与 HTTP 状态码一一对应。
const (
	CodeBadRequest      = "bad_request"
	CodeUnauthorized    = "unauthorized"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeTooLarge        = "payload_too_large"
	CodeTooManyRequests = "too_many_requests"
	CodeInternal        = "internal_error"
)

type errorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error errorInfo `json:"error"`
}

type dataBody struct {
	Data any `json:"data"`
}

// CodeFor 按状态码返回默认错误码。
func CodeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodeTooLarge
	case http.StatusTooManyRequests:
		return CodeTooManyRequests
	default:
		return CodeInternal
	}
}

// JSON 写入任意响应体。
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// security: API 响应可能带用户数据，禁止任何缓存层落盘。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// Data 写入成功响应：{"data": ...}。
func Data(w http.ResponseWriter, status int, v any) {
	JSON(w, status, dataBody{Data: v})
}

// Error 写入失败响应：{"error":{"code":...,"message":...}}。
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, errorBody{Error: errorInfo{Code: code, Message: message}})
}

// Fail 使用状态码对应的默认错误码写入失败响应。
func Fail(w http.ResponseWriter, status int, message string) {
	Error(w, status, CodeFor(status), message)
}
