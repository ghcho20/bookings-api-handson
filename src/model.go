package main

import "time"

// Booking statuses. Only pending and confirmed bookings occupy a slot.
const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

type Resource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Capacity int    `json:"capacity"`
}

type Customer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Booking is both the stored record and the API representation. Timestamps are
// always UTC; notes and cancel_reason render as null when unset.
type Booking struct {
	ID           string    `json:"id"`
	ResourceID   string    `json:"resource_id"`
	Status       string    `json:"status"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Customer     Customer  `json:"customer"`
	Notes        *string   `json:"notes"`
	CancelReason *string   `json:"cancel_reason"`
	CreatedAt    time.Time `json:"created_at"`
}

// Subscription is stored with its secret (a future delivery worker needs it to
// sign payloads) but the secret is only ever returned by the create call.
type Subscription struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Secret    string    `json:"secret,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TimeSlot struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

type Availability struct {
	ResourceID string     `json:"resource_id"`
	Slots      []TimeSlot `json:"slots"`
}

// IdemRecord remembers the first successful POST /bookings for a key: a
// fingerprint of the request and the booking exactly as it was returned.
type IdemRecord struct {
	Fingerprint string  `json:"fingerprint"`
	Booking     Booking `json:"booking"`
}

// State is everything the service persists.
type State struct {
	Version       int                   `json:"version"`
	Resources     []Resource            `json:"resources"`
	Bookings      map[string]*Booking   `json:"bookings"`
	Idempotency   map[string]IdemRecord `json:"idempotency"`
	Subscriptions []Subscription        `json:"subscriptions"`
}

// clone returns a deep copy (used to roll back a failed update).
func (s *State) clone() *State {
	c := &State{
		Version:       s.Version,
		Resources:     append([]Resource(nil), s.Resources...),
		Bookings:      make(map[string]*Booking, len(s.Bookings)),
		Idempotency:   make(map[string]IdemRecord, len(s.Idempotency)),
		Subscriptions: make([]Subscription, len(s.Subscriptions)),
	}
	for id, b := range s.Bookings {
		cp := *b
		c.Bookings[id] = &cp
	}
	for k, v := range s.Idempotency {
		c.Idempotency[k] = v
	}
	for i, sub := range s.Subscriptions {
		sub.Events = append([]string(nil), sub.Events...)
		c.Subscriptions[i] = sub
	}
	return c
}

// normalize makes sure maps and slices that were absent in a loaded file exist.
func (s *State) normalize() {
	if s.Bookings == nil {
		s.Bookings = map[string]*Booking{}
	}
	if s.Idempotency == nil {
		s.Idempotency = map[string]IdemRecord{}
	}
	if s.Subscriptions == nil {
		s.Subscriptions = []Subscription{}
	}
	if s.Resources == nil {
		s.Resources = []Resource{}
	}
}
