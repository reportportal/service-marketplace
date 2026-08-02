package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

func jsonDecode(r *http.Request, v any) error {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return &APIError{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMediaType, Message: "Request media type is not supported"}
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "Request is malformed or is missing a required parameter"}
	}
	return nil
}
