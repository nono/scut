// Package exit defines the exit codes and the JSON error format of scut.
package exit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Code is a process exit code.
type Code int

const (
	OK          Code = 0
	Other       Code = 1
	Usage       Code = 2
	NotFound    Code = 3
	Auth        Code = 4
	Unavailable Code = 5
	Cancelled   Code = 130
)

// Error is an error with an exit code and optional API detail.
type Error struct {
	Code       Code
	Msg        string
	Status     int
	API        json.RawMessage
	Suggestion string
	Valid      []string
	RetryAfter time.Duration // from a Retry-After header; not printed
	NotSent    bool          // a connect failure: the request did not reach the server; not printed
}

func (e *Error) Error() string { return e.Msg }

// Usagef returns a usage error (exit 2).
func Usagef(format string, args ...any) *Error {
	return &Error{Code: Usage, Msg: fmt.Sprintf(format, args...)}
}

// NotFoundf returns a not-found error (exit 3).
func NotFoundf(format string, args ...any) *Error {
	return &Error{Code: NotFound, Msg: fmt.Sprintf(format, args...)}
}

type wire struct {
	Error      string          `json:"error"`
	Status     int             `json:"status,omitempty"`
	API        json.RawMessage `json:"api,omitempty"`
	Suggestion string          `json:"suggestion,omitempty"`
	Valid      []string        `json:"valid,omitempty"`
}

// Write prints err as one JSON object on w and returns its exit code.
// An error that is not an *Error gives code 1.
func Write(w io.Writer, err error) Code {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: Other, Msg: err.Error()}
	}
	b, merr := json.Marshal(wire{e.Msg, e.Status, e.API, e.Suggestion, e.Valid})
	if merr != nil {
		b = []byte(`{"error":"cannot encode error"}`)
	}
	_, _ = w.Write(append(b, '\n'))
	return e.Code
}
