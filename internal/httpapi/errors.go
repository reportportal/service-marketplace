package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
)

type ErrorCode string

const (
	CodeNotFound              ErrorCode = "NOT_FOUND"
	CodeUnauthorized          ErrorCode = "UNAUTHORIZED"
	CodeForbidden             ErrorCode = "FORBIDDEN"
	CodeConflict              ErrorCode = "CONFLICT"
	CodeValidation            ErrorCode = "VALIDATION_ERROR"
	CodeInternal              ErrorCode = "INTERNAL_ERROR"
	CodeStorageConflict       ErrorCode = "STORAGE_CONFLICT"
	CodeStorageUnavailable    ErrorCode = "STORAGE_UNAVAILABLE"
	CodeServiceUnavailable    ErrorCode = "SERVICE_UNAVAILABLE"
	CodePayloadTooLarge       ErrorCode = "PAYLOAD_TOO_LARGE"
	CodeBadRequest            ErrorCode = "BAD_REQUEST"
	CodeUnsupportedMediaType  ErrorCode = "UNSUPPORTED_MEDIA_TYPE"
	CodeTooManyRequests       ErrorCode = "TOO_MANY_REQUESTS"
	CodeCSRFInvalid           ErrorCode = "CSRF_TOKEN_INVALID"
)

type ErrorResponse struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrorResponse struct {
	Code    ErrorCode    `json:"code"`
	Message string       `json:"message"`
	Errors  []FieldError `json:"errors,omitempty"`
}

type APIError struct {
	Status  int
	Code    ErrorCode
	Message string
	Errors  []FieldError
	Headers map[string]string
}

func (e *APIError) Error() string {
	return e.Message
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		for k, v := range apiErr.Headers {
			w.Header().Set(k, v)
		}
		if len(apiErr.Errors) > 0 {
			writeJSON(w, apiErr.Status, ValidationErrorResponse{
				Code:    apiErr.Code,
				Message: apiErr.Message,
				Errors:  apiErr.Errors,
			})
			return
		}
		writeJSON(w, apiErr.Status, ErrorResponse{Code: apiErr.Code, Message: apiErr.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: CodeInternal, Message: "Unexpected registry error"})
}

func clientIP(r *http.Request, trustedProxyHops int) string {
	// Only honor X-Forwarded-For when explicitly behind a trusted proxy.
	if trustedProxyHops > 0 {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			// Take the client IP as seen by the last trusted hop.
			idx := len(parts) - trustedProxyHops
			if idx < 0 {
				idx = 0
			}
			if parts[idx] != "" {
				return parts[idx]
			}
		}
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	return host
}

// isHTTPS reports whether r should be treated as an HTTPS request for the
// purpose of setting the Secure attribute on outgoing cookies. Like
// clientIP, it only honors the forwarded header (X-Forwarded-Proto) once
// trustedProxyHops confirms a trusted proxy sits in front of this process —
// otherwise an unauthenticated caller could assert a scheme the deployment
// never actually terminated (assessment finding
// F5-isHTTPS-trusts-unvalidated-header).
func isHTTPS(r *http.Request, trustedProxyHops int) bool {
	if r.TLS != nil {
		return true
	}
	if trustedProxyHops > 0 && r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}
