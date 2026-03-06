package api

import (
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// Re-export types and functions from the response package
// so existing code using api.OK / api.Fail continues to work.

type Response = response.Response
type ErrorBody = response.ErrorBody

func OK(c echo.Context, data interface{}) error {
	return response.OK(c, data)
}

func Fail(c echo.Context, status int, code, message string) error {
	return response.Fail(c, status, code, message)
}
