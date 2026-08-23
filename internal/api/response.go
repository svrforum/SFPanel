package api

import (
	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// Thin re-exports so callers inside this package can use OK/Fail without
// importing the response package directly.

func OK(c echo.Context, data interface{}) error {
	return response.OK(c, data)
}

func Fail(c echo.Context, status int, code, message string) error {
	return response.Fail(c, status, code, message)
}
