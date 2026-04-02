package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestAPIError_Error_JSON(t *testing.T) {
	e := &APIError{
		Status: 400,
		Body:   `{"error":{"message":"bad request"}}`,
		label:  "test",
	}
	got := e.Error()
	if !strings.Contains(got, "bad request") {
		t.Errorf("Error() should extract 'bad request' from JSON body, got: %q", got)
	}
}

func TestAPIError_Error_NonJSON(t *testing.T) {
	e := &APIError{
		Status: 500,
		Body:   "something went wrong",
		label:  "test",
	}
	got := e.Error()
	if !strings.Contains(got, "500") {
		t.Errorf("Error() should contain status code 500 for non-JSON body, got: %q", got)
	}
	if !strings.Contains(got, "something went wrong") {
		t.Errorf("Error() should contain raw body for non-JSON body, got: %q", got)
	}
}

func TestAPIError_HTTPStatus(t *testing.T) {
	e := &APIError{Status: 400}
	if got := e.HTTPStatus(); got != 400 {
		t.Errorf("HTTPStatus() = %d, want 400", got)
	}
}

func TestIsNotFound(t *testing.T) {
	t.Run("true for 404 APIError", func(t *testing.T) {
		err := &APIError{Status: 404}
		if !IsNotFound(err) {
			t.Error("IsNotFound should return true for APIError with Status 404")
		}
	})

	t.Run("false for 500 APIError", func(t *testing.T) {
		err := &APIError{Status: 500}
		if IsNotFound(err) {
			t.Error("IsNotFound should return false for APIError with Status 500")
		}
	})

	t.Run("false for non-APIError", func(t *testing.T) {
		err := fmt.Errorf("not found")
		if IsNotFound(err) {
			t.Error("IsNotFound should return false for a plain error")
		}
	})
}
