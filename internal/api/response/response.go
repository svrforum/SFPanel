package response

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorBody  `json:"error,omitempty"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details fills in the translated message for the code: which unit, VM,
	// device or repository it is about. The web client hands them to i18next
	// as interpolation values.
	Details map[string]string `json:"details,omitempty"`
}

func OK(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, Response{Success: true, Data: data})
}

// Accepted answers 202 in OK's envelope: what the request started goes on
// after the answer, which does not wait to say how it ended.
func Accepted(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusAccepted, Response{Success: true, Data: data})
}

func Fail(c echo.Context, status int, code, message string) error {
	return c.JSON(status, Response{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: message},
	})
}

// Warning is a problem beside a result that did happen, shown to the operator
// and worded by its code the way an error is.
type Warning struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// RepoWarnings words the repositories apt-get update could not refresh while
// the operation went ahead on the others.
func RepoWarnings(unauthorized, failed []string) []Warning {
	var w []Warning
	for _, uri := range unauthorized {
		w = append(w, Warning{Code: ErrAPTRepoUnauthorized, Message: "repository " + uri + " refused access (401)", Details: map[string]string{"uri": uri}})
	}
	for _, uri := range failed {
		w = append(w, Warning{Code: ErrAPTRepoFailed, Message: "repository " + uri + " could not be refreshed", Details: map[string]string{"uri": uri}})
	}
	return w
}

// FailWith is Fail with the details a translated message names.
func FailWith(c echo.Context, status int, code, message string, details map[string]string) error {
	return c.JSON(status, Response{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: message, Details: details},
	})
}
