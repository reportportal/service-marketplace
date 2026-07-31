package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
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

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
