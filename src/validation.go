package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxBody       = 1 << 20
	maxNotesChars = 1000
	maxReasonChar = 500
	maxWindow     = 14 * 24 * time.Hour
)

var knownEvents = map[string]bool{"booking.confirmed": true, "booking.cancelled": true}

func fieldErr(field, msg string) FieldError { return FieldError{Field: field, Message: msg} }

// parseRFC3339 parses an RFC 3339 timestamp and converts it to UTC.
func parseRFC3339(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}
	at := strings.LastIndex(s, "@")
	return at > 0 && strings.Contains(s[at+1:], ".")
}

// readJSONObject reads the request body as one JSON object. A non-empty detail
// means the request is a 400. An absent body is an error only when required.
func readJSONObject(w http.ResponseWriter, r *http.Request, required bool) (map[string]any, string) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return nil, "The request body could not be read or is larger than 1 MiB."
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if required {
			return nil, "A JSON request body is required."
		}
		return nil, ""
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, "The request body is not valid JSON."
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, "The request body must contain a single JSON value."
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, "The request body must be a JSON object."
	}
	return m, ""
}

// validIdempotencyKey: 8 to 64 printable ASCII characters.
func validIdempotencyKey(key string) string {
	if key == "" {
		return "The Idempotency-Key header is required."
	}
	if len(key) < 8 || len(key) > 64 {
		return "Idempotency-Key must be 8 to 64 characters."
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return "Idempotency-Key must contain only printable ASCII characters."
		}
	}
	return ""
}

func timeField(m map[string]any, name string, errs *[]FieldError) (time.Time, bool) {
	raw, present := m[name]
	if !present || raw == nil {
		*errs = append(*errs, fieldErr(name, name+" is required"))
		return time.Time{}, false
	}
	s, isStr := raw.(string)
	if !isStr {
		*errs = append(*errs, fieldErr(name, name+" must be an RFC 3339 timestamp"))
		return time.Time{}, false
	}
	t, ok := parseRFC3339(s)
	if !ok {
		*errs = append(*errs, fieldErr(name, name+" must be an RFC 3339 timestamp"))
		return time.Time{}, false
	}
	return t, true
}

// optionalString reads an optional string field of at most max characters.
func optionalString(m map[string]any, name string, max int, errs *[]FieldError) *string {
	raw, present := m[name]
	if !present || raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		*errs = append(*errs, fieldErr(name, name+" must be a string"))
		return nil
	}
	if utf8.RuneCountInString(s) > max {
		*errs = append(*errs, fieldErr(name, name+" must be at most "+itoa(max)+" characters"))
		return nil
	}
	return &s
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func parseBookingCreate(m map[string]any) (BookingInput, []FieldError) {
	var errs []FieldError
	var in BookingInput
	if rid, ok := m["resource_id"].(string); !ok || strings.TrimSpace(rid) == "" {
		errs = append(errs, fieldErr("resource_id", "resource_id is required"))
	} else {
		in.ResourceID = rid
	}
	start, startOK := timeField(m, "start_time", &errs)
	end, endOK := timeField(m, "end_time", &errs)
	if startOK && endOK && !end.After(start) {
		errs = append(errs, fieldErr("end_time", "end_time must be after start_time"))
	}
	in.Start, in.End = start, end
	cust, ok := m["customer"].(map[string]any)
	if !ok {
		errs = append(errs, fieldErr("customer", "customer is required"))
	} else {
		if name, ok := cust["name"].(string); !ok || strings.TrimSpace(name) == "" {
			errs = append(errs, fieldErr("customer.name", "customer.name is required"))
		} else {
			in.Customer.Name = name
		}
		if email, ok := cust["email"].(string); !ok || !validEmail(email) {
			errs = append(errs, fieldErr("customer.email", "customer.email must be an email address"))
		} else {
			in.Customer.Email = email
		}
	}
	in.Notes = optionalString(m, "notes", maxNotesChars, &errs)
	return in, errs
}

func parseBookingUpdate(m map[string]any) (BookingPatch, []FieldError) {
	var errs []FieldError
	var p BookingPatch
	if m == nil {
		m = map[string]any{}
	}
	if _, ok := m["start_time"]; ok {
		if t, ok := timeField(m, "start_time", &errs); ok {
			p.Start = &t
		}
	}
	if _, ok := m["end_time"]; ok {
		if t, ok := timeField(m, "end_time", &errs); ok {
			p.End = &t
		}
	}
	if _, ok := m["notes"]; ok {
		if m["notes"] == nil {
			errs = append(errs, fieldErr("notes", "notes must be a string"))
		} else {
			p.Notes = optionalString(m, "notes", maxNotesChars, &errs)
		}
	}
	if len(errs) == 0 && p.Start == nil && p.End == nil && p.Notes == nil {
		errs = append(errs, fieldErr("body", "provide at least one of start_time, end_time, notes"))
	}
	return p, errs
}

func parseCancel(m map[string]any) (*string, []FieldError) {
	var errs []FieldError
	reason := optionalString(m, "reason", maxReasonChar, &errs)
	return reason, errs
}

func parseSubscription(m map[string]any) (string, []string, []FieldError) {
	var errs []FieldError
	rawURL, _ := m["url"].(string)
	if u, err := url.Parse(rawURL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fieldErr("url", "url must be an HTTPS URL"))
	}
	var events []string
	list, ok := m["events"].([]any)
	if !ok || len(list) == 0 {
		errs = append(errs, fieldErr("events", "events must be a non-empty list of booking.confirmed, booking.cancelled"))
	} else {
		seen := map[string]bool{}
		for _, e := range list {
			name, isStr := e.(string)
			if !isStr || !knownEvents[name] {
				errs = append(errs, fieldErr("events", "events must be a non-empty list of booking.confirmed, booking.cancelled"))
				events = nil
				break
			}
			if !seen[name] {
				seen[name] = true
				events = append(events, name)
			}
		}
	}
	return rawURL, events, errs
}
