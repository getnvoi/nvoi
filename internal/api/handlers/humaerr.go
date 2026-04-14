package handlers

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// apiError is our error model. Matches the existing `{"error": "..."}` format
// so the cloud CLI and tests don't break.
type apiError struct {
	status int
	Err    string `json:"error"`
}

func (e *apiError) Error() string  { return e.Err }
func (e *apiError) GetStatus() int { return e.status }

// humaError maps pkg/core error types to appropriate HTTP status codes.
// NotFoundError → 404, InputError → 400, NotReadyError → 422, everything else → 500.
func humaError(err error) error {
	var notFound *pkgcore.NotFoundError
	var input *pkgcore.InputError
	var notReady *pkgcore.NotReadyError
	switch {
	case errors.As(err, &notFound):
		return huma.Error404NotFound(err.Error())
	case errors.As(err, &input):
		return huma.Error400BadRequest(err.Error())
	case errors.As(err, &notReady):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		return huma.Error500InternalServerError(err.Error())
	}
}

func init() {
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		// For validation errors, concatenate details into the message.
		if len(errs) > 0 && msg == "" {
			msg = errs[0].Error()
		}
		if msg == "" {
			msg = http.StatusText(status)
		}
		return &apiError{status: status, Err: msg}
	}
}
