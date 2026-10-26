package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRespondError(t *testing.T) {
	recorder := httptest.NewRecorder()
	RespondError(recorder, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid", map[string]any{"field": "email"})

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}

	expectedBody := `{"error":{"code":"INVALID_REQUEST","message":"The request is invalid","params":{"field":"email"}}}` + "\n"
	if recorder.Body.String() != expectedBody {
		t.Errorf("expected body %s, got %s", expectedBody, recorder.Body.String())
	}
}
