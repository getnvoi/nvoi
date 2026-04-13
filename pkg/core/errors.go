package core

import "fmt"

// NotFoundError indicates a resource was not found. API handlers map this to 404.
type NotFoundError struct {
	Resource string
	Name     string
}

func (e *NotFoundError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("%s %q not found", e.Resource, e.Name)
	}
	return fmt.Sprintf("%s not found", e.Resource)
}

// ErrNotFound creates a NotFoundError.
func ErrNotFound(resource, name string) error {
	return &NotFoundError{Resource: resource, Name: name}
}

// InputError indicates invalid user input. API handlers map this to 400.
type InputError struct {
	Message string
}

func (e *InputError) Error() string { return e.Message }

// ErrInput creates an InputError.
func ErrInput(msg string) error {
	return &InputError{Message: msg}
}

// ErrInputf creates a formatted InputError.
func ErrInputf(format string, args ...any) error {
	return &InputError{Message: fmt.Sprintf(format, args...)}
}

// NotReadyError indicates a resource exists but isn't deployed/configured yet.
// API handlers map this to 422.
type NotReadyError struct {
	Message string
}

func (e *NotReadyError) Error() string { return e.Message }

// ErrNotReady creates a NotReadyError.
func ErrNotReady(msg string) error {
	return &NotReadyError{Message: msg}
}
