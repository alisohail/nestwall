package web

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents the consistent error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the details of an error.
type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

// RespondError sends a JSON error response.
func RespondError(w http.ResponseWriter, status int, code, message string, params map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Params:  params,
		},
	})
}

// RespondJSON sends a JSON response.
func RespondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
