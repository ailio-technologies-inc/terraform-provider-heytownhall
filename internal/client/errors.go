package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// APIError is the API's error envelope: {"error":{"code","message","param","request_id"}}.
type APIError struct {
	Status    int
	Code      string
	Message   string
	Param     string
	RequestID string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP %d", e.Status)
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Param != "" {
		fmt.Fprintf(&b, " (parameter: %s)", e.Param)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request id: %s)", e.RequestID)
	}
	return b.String()
}

func parseAPIError(status int, raw []byte) *APIError {
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Param     string `json:"param"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	e := &APIError{Status: status}
	if json.Unmarshal(raw, &env) == nil && (env.Error.Code != "" || env.Error.Message != "") {
		e.Code, e.Message, e.Param, e.RequestID = env.Error.Code, env.Error.Message, env.Error.Param, env.Error.RequestID
		return e
	}
	// Not the envelope (a proxy page, say). Keep a short piece so the cause is visible.
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	e.Message = s
	return e
}

// AsAPIError returns the API error in err's chain.
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsNotFound reports whether the API answered 404.
func IsNotFound(err error) bool {
	e, ok := AsAPIError(err)
	return ok && e.Status == 404
}

// HasCode reports whether the API answered with this error code.
func HasCode(err error, code string) bool {
	e, ok := AsAPIError(err)
	return ok && e.Code == code
}
