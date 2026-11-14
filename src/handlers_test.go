package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fullKey = "test-full-key"
	roKey   = "test-readonly-key"
)

func newTestServer(t *testing.T, testRoutes bool) http.Handler {
	t.Helper()
	cfg := Config{Port: "0", DataFile: filepath.Join(t.TempDir(), "state.json"),
		APIKeys: []string{fullKey}, ReadOnlyKeys: []string{roKey}, EnableTestRoutes: testRoutes}
	store, err := OpenStore(cfg.DataFile)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(store, cfg, log.New(io.Discard, "", 0)).Handler()
}

func do(h http.Handler, method, path, key, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, typeSuffix string) map[string]any {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	m := decode(t, rec)
	if typ, _ := m["type"].(string); !strings.HasSuffix(typ, typeSuffix) {
		t.Errorf("type = %q, want suffix %q", typ, typeSuffix)
	}
	return m
}

const validBody = `{"resource_id":"res_8b44d0","start_time":"2026-12-08T09:00:00Z","end_time":"2026-12-08T10:00:00Z","customer":{"name":"QA","email":"qa@example.com"}}`

func idem(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }

func TestAuthIsCheckedBeforeBodyValidation(t *testing.T) {
	h := newTestServer(t, true)
	// An invalid body with no key is 401, not 400 or 422.
	rec := do(h, "POST", "/v1/bookings", "", `{"end_time":"bad"}`, nil)
	wantProblem(t, rec, 401, "unauthorized")
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 needs a WWW-Authenticate header")
	}
	wantProblem(t, do(h, "GET", "/v1/resources", "unknown-key", "", nil), 401, "unauthorized")
	wantProblem(t, do(h, "GET", "/v1/resources", "", "", map[string]string{"Authorization": "Bearer abc"}), 401, "unauthorized")
}

func TestReadOnlyKey(t *testing.T) {
	h := newTestServer(t, true)
	if rec := do(h, "GET", "/v1/bookings", roKey, "", nil); rec.Code != 200 {
		t.Errorf("read with read-only key: %d", rec.Code)
	}
	wantProblem(t, do(h, "POST", "/v1/bookings/bkg_9d2e47/confirm", roKey, "", nil), 403, "insufficient-scope")
	// 403 comes before body validation too.
	wantProblem(t, do(h, "POST", "/v1/bookings", roKey, "not json", idem("key-00001")), 403, "insufficient-scope")
}

func TestUnknownRouteAndWrongMethod(t *testing.T) {
	h := newTestServer(t, true)
	wantProblem(t, do(h, "GET", "/v1/nothing", fullKey, "", nil), 404, "not-found")
	rec := do(h, "DELETE", "/v1/bookings/bkg_9d2e47", fullKey, "", nil)
	wantProblem(t, rec, 405, "method-not-allowed")
	if allow := rec.Header().Get("Allow"); allow != "GET, PATCH" {
		t.Errorf("Allow = %q", allow)
	}
	if rec := do(h, "GET", "/healthz", "", "", nil); rec.Code != 200 {
		t.Errorf("healthz = %d", rec.Code)
	}
}

func TestResetRouteOnlyWhenEnabled(t *testing.T) {
	on := newTestServer(t, true)
	rec := do(on, "POST", "/v1/__mock/reset", "", "", nil)
	if rec.Code != 200 || decode(t, rec)["reset"] != true {
		t.Errorf("reset: %d %s", rec.Code, rec.Body.String())
	}
	off := newTestServer(t, false)
	wantProblem(t, do(off, "POST", "/v1/__mock/reset", "", "", nil), 404, "not-found")
}

func TestCreateGetAndIdempotentReplay(t *testing.T) {
	h := newTestServer(t, true)
	rec := do(h, "POST", "/v1/bookings", fullKey, validBody, idem("key-00001"))
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	created := decode(t, rec)
	id := created["id"].(string)
	if created["status"] != "pending" || rec.Header().Get("Location") != "/v1/bookings/"+id {
		t.Errorf("created: %v location=%q", created, rec.Header().Get("Location"))
	}
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Error("first create is not a replay")
	}

	if got := decode(t, do(h, "GET", "/v1/bookings/"+id, fullKey, "", nil)); got["id"] != id {
		t.Errorf("get: %v", got)
	}

	rep := do(h, "POST", "/v1/bookings", fullKey, validBody, idem("key-00001"))
	if rep.Code != 201 || rep.Header().Get("Idempotent-Replayed") != "true" || decode(t, rep)["id"] != id {
		t.Errorf("replay: %d %v replayed=%q", rep.Code, rep.Body.String(), rep.Header().Get("Idempotent-Replayed"))
	}

	other := strings.Replace(validBody, "09:00:00Z", "13:00:00Z", 1)
	other = strings.Replace(other, "10:00:00Z", "14:00:00Z", 1)
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, other, idem("key-00001")), 409, "idempotency-key-reuse")
}

func TestCreateValidation(t *testing.T) {
	h := newTestServer(t, true)
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, validBody, nil), 400, "bad-request")             // no key
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, validBody, idem("short")), 400, "bad-request")   // too short
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, "{oops", idem("key-00001")), 400, "bad-request") // bad JSON
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, "[1]", idem("key-00001")), 400, "bad-request")   // not an object
	reversed := strings.NewReplacer("09:00:00Z", "11:00:00Z").Replace(validBody)
	m := wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, reversed, idem("key-00002")), 422, "validation-error")
	errs, _ := m["errors"].([]any)
	if len(errs) == 0 || errs[0].(map[string]any)["field"] != "end_time" {
		t.Errorf("errors = %v", m["errors"])
	}
	badMail := strings.Replace(validBody, "qa@example.com", "nope", 1)
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, badMail, idem("key-00003")), 422, "validation-error")
	longNotes := strings.Replace(validBody, `"customer"`, `"notes":"`+strings.Repeat("x", 1001)+`","customer"`, 1)
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, longNotes, idem("key-00004")), 422, "validation-error")
	unknown := strings.Replace(validBody, "res_8b44d0", "res_does_not_exist", 1)
	wantProblem(t, do(h, "POST", "/v1/bookings", fullKey, unknown, idem("key-00005")), 404, "not-found")
}

func TestLifecycleOverHTTP(t *testing.T) {
	h := newTestServer(t, true)
	if got := decode(t, do(h, "POST", "/v1/bookings/bkg_9d2e47/confirm", fullKey, "", nil)); got["status"] != "confirmed" {
		t.Fatalf("confirm: %v", got)
	}
	wantProblem(t, do(h, "POST", "/v1/bookings/bkg_9d2e47/confirm", fullKey, "", nil), 409, "invalid-transition")
	if got := decode(t, do(h, "POST", "/v1/bookings/bkg_9d2e47/cancel", fullKey, `{"reason":"moved"}`, nil)); got["status"] != "cancelled" || got["cancel_reason"] != "moved" {
		t.Fatalf("cancel: %v", got)
	}
	wantProblem(t, do(h, "POST", "/v1/bookings/bkg_9d2e47/cancel", fullKey, "", nil), 409, "invalid-transition")
	wantProblem(t, do(h, "POST", "/v1/bookings/bkg_nope/confirm", fullKey, "", nil), 404, "not-found")
	wantProblem(t, do(h, "PATCH", "/v1/bookings/bkg_9d2e47", fullKey, `{"notes":"x"}`, nil), 409, "invalid-transition")
	wantProblem(t, do(h, "PATCH", "/v1/bookings/bkg_c0f1a2", fullKey, `{}`, nil), 422, "validation-error")
}

func TestAvailabilityWindow(t *testing.T) {
	h := newTestServer(t, true)
	ok := do(h, "GET", "/v1/resources/res_3f9a1c/availability?from=2026-12-07T09:00:00Z&to=2026-12-21T09:00:00Z", fullKey, "", nil)
	if ok.Code != 200 {
		t.Errorf("14 days exactly is allowed: %d %s", ok.Code, ok.Body.String())
	}
	wantProblem(t, do(h, "GET", "/v1/resources/res_3f9a1c/availability?from=2026-12-07T09:00:00Z&to=2026-12-21T09:00:01Z", fullKey, "", nil), 400, "bad-request")
	wantProblem(t, do(h, "GET", "/v1/resources/res_3f9a1c/availability?from=2026-12-07T09:00:00Z", fullKey, "", nil), 400, "bad-request")
	wantProblem(t, do(h, "GET", "/v1/resources/res_nope/availability?from=2026-12-07T09:00:00Z&to=2026-12-07T18:00:00Z", fullKey, "", nil), 404, "not-found")
}

func TestListsAndPagination(t *testing.T) {
	h := newTestServer(t, true)
	page := decode(t, do(h, "GET", "/v1/bookings?limit=3", fullKey, "", nil))
	if len(page["data"].([]any)) != 3 || page["next_cursor"] == nil {
		t.Fatalf("first page: %v", page)
	}
	// Newest first: bkg_9d2e47 (2026-11-03) leads the seed.
	if page["data"].([]any)[0].(map[string]any)["id"] != "bkg_9d2e47" {
		t.Errorf("order: %v", page["data"])
	}
	last := decode(t, do(h, "GET", "/v1/bookings?limit=3&cursor="+page["next_cursor"].(string), fullKey, "", nil))
	if len(last["data"].([]any)) != 1 || last["next_cursor"] != nil {
		t.Errorf("last page: %v", last)
	}
	beyond := decode(t, do(h, "GET", "/v1/bookings?cursor=eyJvIjoyMH0", fullKey, "", nil))
	if d, ok := beyond["data"].([]any); !ok || len(d) != 0 {
		t.Errorf("past the end must be an empty list: %v", beyond)
	}
	wantProblem(t, do(h, "GET", "/v1/bookings?limit=0", fullKey, "", nil), 400, "bad-request")
	wantProblem(t, do(h, "GET", "/v1/bookings?cursor=%21%21", fullKey, "", nil), 400, "bad-request")
	wantProblem(t, do(h, "GET", "/v1/bookings?status=bogus", fullKey, "", nil), 400, "bad-request")
	filtered := decode(t, do(h, "GET", "/v1/bookings?status=cancelled", fullKey, "", nil))
	if len(filtered["data"].([]any)) != 1 {
		t.Errorf("status filter: %v", filtered)
	}
}

func TestWebhookSubscription(t *testing.T) {
	h := newTestServer(t, true)
	rec := do(h, "POST", "/v1/webhook-subscriptions", fullKey, `{"url":"https://hooks.example.com/b","events":["booking.confirmed","booking.confirmed"]}`, nil)
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	if !strings.HasPrefix(m["secret"].(string), "whsec_") || len(m["events"].([]any)) != 1 {
		t.Errorf("subscription: %v", m)
	}
	wantProblem(t, do(h, "POST", "/v1/webhook-subscriptions", fullKey, `{"url":"http://hooks.example.com","events":["booking.confirmed"]}`, nil), 422, "validation-error")
	wantProblem(t, do(h, "POST", "/v1/webhook-subscriptions", fullKey, `{"url":"https://hooks.example.com","events":["nope"]}`, nil), 422, "validation-error")
}
