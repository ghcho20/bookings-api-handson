package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Server wires the store, auth and routes together.
type Server struct {
	store *Store
	cfg   Config
	auth  *Authenticator
	log   *log.Logger
	now   func() time.Time
}

func NewServer(store *Store, cfg Config, logger *log.Logger) *Server {
	return &Server{store: store, cfg: cfg, auth: NewAuthenticator(cfg), log: logger, now: time.Now}
}

type methods map[string]http.HandlerFunc

// handle registers one path. Any method not in m is a 405 with Allow, as problem+json.
func handle(mux *http.ServeMux, pattern string, m methods) {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	allow := strings.Join(names, ", ")
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		h, ok := m[r.Method]
		if !ok {
			writeProblem(w, http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed",
				"This path supports: "+allow+".", nil, map[string]string{"Allow": allow})
			return
		}
		h(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		notFound(w, "No route for "+r.Method+" "+r.URL.Path+".")
	})
	read := func(h http.HandlerFunc) http.HandlerFunc { return s.auth.guard(false, h) }
	write := func(h http.HandlerFunc) http.HandlerFunc { return s.auth.guard(true, h) }

	handle(mux, "/healthz", methods{"GET": s.healthz})
	handle(mux, "/v1/resources", methods{"GET": read(s.listResources)})
	handle(mux, "/v1/resources/{resource_id}", methods{"GET": read(s.getResource)})
	handle(mux, "/v1/resources/{resource_id}/availability", methods{"GET": read(s.getAvailability)})
	handle(mux, "/v1/bookings", methods{"GET": read(s.listBookings), "POST": write(s.createBooking)})
	handle(mux, "/v1/bookings/{booking_id}", methods{"GET": read(s.getBooking), "PATCH": write(s.rescheduleBooking)})
	handle(mux, "/v1/bookings/{booking_id}/confirm", methods{"POST": write(s.confirmBooking)})
	handle(mux, "/v1/bookings/{booking_id}/cancel", methods{"POST": write(s.cancelBooking)})
	handle(mux, "/v1/webhook-subscriptions", methods{"POST": write(s.createSubscription)})
	if s.cfg.EnableTestRoutes {
		// Test support only: needs no API key, like the mock's reset route.
		handle(mux, "/v1/__mock/reset", methods{"POST": s.reset})
	}
	return s.recoverer(s.logRequests(mux))
}

// ---- middleware

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Microsecond))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Printf("panic serving %s %s: %v", r.Method, r.URL.Path, rec)
				internalError(w)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// writeDomainError maps a domain error to its problem response.
func (s *Server) writeDomainError(w http.ResponseWriter, err error) {
	var (
		nf   *NotFoundError
		tr   *TransitionError
		slot *SlotConflictError
		val  *ValidationError
		pe   *PersistError
	)
	switch {
	case errors.As(err, &nf):
		notFound(w, nf.Error())
	case errors.As(err, &tr):
		writeProblem(w, http.StatusConflict, "invalid-transition", "Invalid status transition", tr.Error(), nil, nil)
	case errors.As(err, &slot):
		writeProblem(w, http.StatusConflict, "slot-taken", "Slot already booked", slot.Error(), nil, nil)
	case errors.Is(err, ErrIdempotencyReuse):
		writeProblem(w, http.StatusConflict, "idempotency-key-reuse", "Idempotency key reused",
			"This Idempotency-Key was already used with a different request body.", nil, nil)
	case errors.As(err, &val):
		unprocessable(w, val.Errors)
	case errors.As(err, &pe):
		s.log.Printf("error: %v", pe)
		internalError(w)
	default:
		s.log.Printf("error: %v", err)
		internalError(w)
	}
}

// ---- handlers

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	ids, err := s.store.Reset()
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reset": true, "bookings": ids})
}

func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pageParams(q)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	var items []Resource
	s.store.View(func(st *State) {
		for _, res := range st.Resources {
			if t := q.Get("type"); t != "" && res.Type != t {
				continue
			}
			if l := q.Get("location"); l != "" && res.Location != l {
				continue
			}
			items = append(items, res)
		}
	})
	page, next := paginate(items, limit, offset)
	writeJSON(w, http.StatusOK, Page[Resource]{Data: page, NextCursor: next})
}

func (s *Server) getResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("resource_id")
	var res *Resource
	s.store.View(func(st *State) {
		if found := st.FindResource(id); found != nil {
			cp := *found
			res = &cp
		}
	})
	if res == nil {
		notFound(w, (&NotFoundError{"resource", id}).Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getAvailability(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("resource_id")
	q := r.URL.Query()
	from, okFrom := parseRFC3339(q.Get("from"))
	if !okFrom {
		badRequest(w, "Query parameter 'from' must be an RFC 3339 timestamp.")
		return
	}
	to, okTo := parseRFC3339(q.Get("to"))
	if !okTo {
		badRequest(w, "Query parameter 'to' must be an RFC 3339 timestamp.")
		return
	}
	if !to.After(from) {
		badRequest(w, "Query parameter 'to' must be after 'from'.")
		return
	}
	if to.Sub(from) > maxWindow {
		badRequest(w, "The window between 'from' and 'to' may not exceed 14 days.")
		return
	}
	var out *Availability
	s.store.View(func(st *State) {
		if st.FindResource(id) != nil {
			out = &Availability{ResourceID: id, Slots: st.FreeSlots(id, from, to)}
		}
	})
	if out == nil {
		notFound(w, (&NotFoundError{"resource", id}).Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listBookings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pageParams(q)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if st := q.Get("status"); st != "" && st != StatusPending && st != StatusConfirmed && st != StatusCompleted && st != StatusCancelled {
		badRequest(w, "Query parameter 'status' must be one of pending, confirmed, completed, cancelled.")
		return
	}
	if e := q.Get("customer_email"); e != "" && !validEmail(e) {
		badRequest(w, "Query parameter 'customer_email' must be an email address.")
		return
	}
	items := s.filterBookings(q)
	page, next := paginate(items, limit, offset)
	writeJSON(w, http.StatusOK, Page[Booking]{Data: page, NextCursor: next})
}

// filterBookings returns the matching bookings, newest first (ties by id, so
// the order is total and cursors stay stable).
func (s *Server) filterBookings(q url.Values) []Booking {
	var items []Booking
	s.store.View(func(st *State) {
		for _, b := range st.Bookings {
			if v := q.Get("status"); v != "" && b.Status != v {
				continue
			}
			if v := q.Get("resource_id"); v != "" && b.ResourceID != v {
				continue
			}
			if v := q.Get("customer_email"); v != "" && !strings.EqualFold(b.Customer.Email, v) {
				continue
			}
			items = append(items, *b)
		}
	})
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID > items[j].ID
	})
	return items
}

func (s *Server) createBooking(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if msg := validIdempotencyKey(key); msg != "" {
		badRequest(w, msg)
		return
	}
	body, detail := readJSONObject(w, r, true)
	if detail != "" {
		badRequest(w, detail)
		return
	}
	in, errs := parseBookingCreate(body)
	if len(errs) > 0 {
		unprocessable(w, errs)
		return
	}
	var created Booking
	var replayed bool
	err := s.store.Update(func(st *State) (bool, error) {
		b, rep, err := st.CreateBooking(key, in, s.now())
		if err != nil {
			return false, err
		}
		created, replayed = b, rep
		return !rep, nil
	})
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	headers := map[string]string{"Location": "/v1/bookings/" + created.ID}
	if replayed {
		headers["Idempotent-Replayed"] = "true"
	}
	writeBody(w, http.StatusCreated, "application/json", created, headers)
}

func (s *Server) getBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("booking_id")
	var out *Booking
	s.store.View(func(st *State) {
		if b, ok := st.Bookings[id]; ok {
			cp := *b
			out = &cp
		}
	})
	if out == nil {
		notFound(w, (&NotFoundError{"booking", id}).Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) rescheduleBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("booking_id")
	body, detail := readJSONObject(w, r, true)
	if detail != "" {
		badRequest(w, detail)
		return
	}
	patch, errs := parseBookingUpdate(body)
	if len(errs) > 0 {
		unprocessable(w, errs)
		return
	}
	var out Booking
	err := s.store.Update(func(st *State) (bool, error) {
		b, err := st.Reschedule(id, patch)
		out = b
		return err == nil, err
	})
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) confirmBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("booking_id")
	var out Booking
	err := s.store.Update(func(st *State) (bool, error) {
		b, err := st.ConfirmBooking(id)
		out = b
		return err == nil, err
	})
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cancelBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("booking_id")
	body, detail := readJSONObject(w, r, false)
	if detail != "" {
		badRequest(w, detail)
		return
	}
	reason, errs := parseCancel(body)
	if len(errs) > 0 {
		unprocessable(w, errs)
		return
	}
	var out Booking
	err := s.store.Update(func(st *State) (bool, error) {
		b, err := st.CancelBooking(id, reason)
		out = b
		return err == nil, err
	})
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	body, detail := readJSONObject(w, r, true)
	if detail != "" {
		badRequest(w, detail)
		return
	}
	target, events, errs := parseSubscription(body)
	if len(errs) > 0 {
		unprocessable(w, errs)
		return
	}
	var sub Subscription
	err := s.store.Update(func(st *State) (bool, error) {
		sub = st.AddSubscription(target, events, s.now())
		return true, nil
	})
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}
