package main

import (
	"encoding/json"
	"net/http"
)

const problemBase = "https://api.bookings.example.com/problems/"

// FieldError is one entry of a 422 problem's errors list.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Problem is an RFC 9457 problem document.
type Problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Errors []FieldError `json:"errors,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	writeBody(w, status, "application/json", v, nil)
}

func writeBody(w http.ResponseWriter, status int, contentType string, v any, headers map[string]string) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for k, val := range headers {
		w.Header().Set(k, val)
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

func writeProblem(w http.ResponseWriter, status int, slug, title, detail string, errs []FieldError, headers map[string]string) {
	writeBody(w, status, "application/problem+json", Problem{
		Type: problemBase + slug, Title: title, Status: status, Detail: detail, Errors: errs,
	}, headers)
}

func badRequest(w http.ResponseWriter, detail string) {
	writeProblem(w, http.StatusBadRequest, "bad-request", "Bad request", detail, nil, nil)
}

func unauthorized(w http.ResponseWriter) {
	writeProblem(w, http.StatusUnauthorized, "unauthorized", "Unauthorized",
		"A valid X-API-Key header is required.", nil,
		map[string]string{"WWW-Authenticate": `Bearer realm="bookings-api"`})
}

func forbidden(w http.ResponseWriter) {
	writeProblem(w, http.StatusForbidden, "insufficient-scope", "Insufficient scope",
		"This API key is read-only; the operation requires the 'bookings:write' scope.", nil, nil)
}

func notFound(w http.ResponseWriter, detail string) {
	writeProblem(w, http.StatusNotFound, "not-found", "Not found", detail, nil, nil)
}

func unprocessable(w http.ResponseWriter, errs []FieldError) {
	writeProblem(w, http.StatusUnprocessableEntity, "validation-error", "Validation failed", "", errs, nil)
}

func internalError(w http.ResponseWriter) {
	writeProblem(w, http.StatusInternalServerError, "internal-server-error", "Internal server error",
		"An unexpected error occurred. Please try again later.", nil, nil)
}
