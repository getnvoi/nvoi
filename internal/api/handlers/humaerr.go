package handlers

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// apiError is our error model. Matches the existing `{"error": "..."}` format
// so the cloud CLI and tests don't break.
type apiError struct {
	status int
	Err    string `json:"error"`
}

func (e *apiError) Error() string  { return e.Err }
func (e *apiError) GetStatus() int { return e.status }

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
