package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ---- errors the HTTP layer maps to problem responses

type NotFoundError struct{ Kind, ID string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("No %s with id %s.", e.Kind, e.ID) }

type TransitionError struct{ BookingID, Status, Action string }

func (e *TransitionError) Error() string {
	return fmt.Sprintf("Booking %s is %s and cannot be %s.", e.BookingID, e.Status, e.Action)
}

type SlotConflictError struct {
	ResourceID string
	Start, End time.Time
}

func (e *SlotConflictError) Error() string {
	return fmt.Sprintf("Resource %s is not free between %s and %s.", e.ResourceID, fmtTime(e.Start), fmtTime(e.End))
}

type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string { return "validation failed" }

// ErrIdempotencyReuse: the same Idempotency-Key was sent with a different request.
var ErrIdempotencyReuse = errors.New("idempotency key was already used with a different request")

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ---- slot logic

// overlaps reports whether two half-open intervals [aStart, aEnd) and
// [bStart, bEnd) share any time. Back-to-back intervals do not overlap.
func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// occupiesSlot: only pending and confirmed bookings hold their slot.
func occupiesSlot(status string) bool {
	return status == StatusPending || status == StatusConfirmed
}

func (s *State) FindResource(id string) *Resource {
	for i := range s.Resources {
		if s.Resources[i].ID == id {
			return &s.Resources[i]
		}
	}
	return nil
}

// SlotFree reports whether [start, end) on the resource is free of other
// occupying bookings. ignoreID excludes one booking (used when rescheduling it).
func (s *State) SlotFree(resourceID string, start, end time.Time, ignoreID string) bool {
	for _, b := range s.Bookings {
		if b.ID == ignoreID || b.ResourceID != resourceID || !occupiesSlot(b.Status) {
			continue
		}
		if overlaps(start, end, b.StartTime, b.EndTime) {
			return false
		}
	}
	return true
}

// FreeSlots returns the free intervals of a resource inside [from, to).
func (s *State) FreeSlots(resourceID string, from, to time.Time) []TimeSlot {
	var busy []TimeSlot
	for _, b := range s.Bookings {
		if b.ResourceID != resourceID || !occupiesSlot(b.Status) || !overlaps(from, to, b.StartTime, b.EndTime) {
			continue
		}
		st, en := b.StartTime, b.EndTime
		if st.Before(from) {
			st = from
		}
		if en.After(to) {
			en = to
		}
		busy = append(busy, TimeSlot{StartTime: st, EndTime: en})
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].StartTime.Before(busy[j].StartTime) })
	slots := []TimeSlot{}
	cursor := from
	for _, b := range busy {
		if b.StartTime.After(cursor) {
			slots = append(slots, TimeSlot{StartTime: cursor, EndTime: b.StartTime})
		}
		if b.EndTime.After(cursor) {
			cursor = b.EndTime
		}
	}
	if cursor.Before(to) {
		slots = append(slots, TimeSlot{StartTime: cursor, EndTime: to})
	}
	return slots
}

// ---- state machine: pending -> confirmed -> completed; cancel from pending or confirmed

func (b *Booking) Confirm() error {
	if b.Status != StatusPending {
		return &TransitionError{b.ID, b.Status, "confirmed"}
	}
	b.Status = StatusConfirmed
	return nil
}

func (b *Booking) Cancel(reason *string) error {
	if !occupiesSlot(b.Status) {
		return &TransitionError{b.ID, b.Status, "cancelled"}
	}
	b.Status = StatusCancelled
	b.CancelReason = reason
	return nil
}

// Complete moves confirmed -> completed. Nothing in the API calls it: the spec
// completes a booking when its end_time passes, and that is not automated here
// (see README notes in the step report).
func (b *Booking) Complete() error {
	if b.Status != StatusConfirmed {
		return &TransitionError{b.ID, b.Status, "completed"}
	}
	b.Status = StatusCompleted
	return nil
}

func (b *Booking) canReschedule() error {
	if !occupiesSlot(b.Status) {
		return &TransitionError{b.ID, b.Status, "rescheduled"}
	}
	return nil
}

// ---- operations on the state (callers hold the store lock)

type BookingInput struct {
	ResourceID string
	Start, End time.Time
	Customer   Customer
	Notes      *string
}

// fingerprint identifies a create request by value, so key order, time zone
// offsets and whitespace in the JSON do not matter.
func (in BookingInput) fingerprint() string {
	b, _ := json.Marshal(struct {
		Resource string   `json:"r"`
		Start    string   `json:"s"`
		End      string   `json:"e"`
		Customer Customer `json:"c"`
		Notes    *string  `json:"n"`
	}{in.ResourceID, fmtTime(in.Start), fmtTime(in.End), in.Customer, in.Notes})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randomID(prefix string) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failing is not recoverable
	}
	return prefix + hex.EncodeToString(b[:])
}

func (s *State) newBookingID() string {
	for {
		id := randomID("bkg_")
		if _, taken := s.Bookings[id]; !taken {
			return id
		}
	}
}

// CreateBooking creates a pending booking. A repeated key with the same request
// returns the original booking (replayed=true); with a different request it
// fails with ErrIdempotencyReuse. Failed creates record nothing, so a retry
// with the same key can still succeed.
func (s *State) CreateBooking(key string, in BookingInput, now time.Time) (Booking, bool, error) {
	fp := in.fingerprint()
	if rec, ok := s.Idempotency[key]; ok {
		if rec.Fingerprint == fp {
			return rec.Booking, true, nil
		}
		return Booking{}, false, ErrIdempotencyReuse
	}
	if s.FindResource(in.ResourceID) == nil {
		return Booking{}, false, &NotFoundError{"resource", in.ResourceID}
	}
	if !s.SlotFree(in.ResourceID, in.Start, in.End, "") {
		return Booking{}, false, &SlotConflictError{in.ResourceID, in.Start, in.End}
	}
	b := &Booking{
		ID: s.newBookingID(), ResourceID: in.ResourceID, Status: StatusPending,
		StartTime: in.Start, EndTime: in.End, Customer: in.Customer, Notes: in.Notes,
		CreatedAt: now.UTC().Truncate(time.Microsecond),
	}
	s.Bookings[b.ID] = b
	s.Idempotency[key] = IdemRecord{Fingerprint: fp, Booking: *b}
	return *b, false, nil
}

type BookingPatch struct {
	Start, End *time.Time
	Notes      *string
}

// Reschedule changes the time range and/or notes of a pending or confirmed booking.
func (s *State) Reschedule(id string, p BookingPatch) (Booking, error) {
	b, ok := s.Bookings[id]
	if !ok {
		return Booking{}, &NotFoundError{"booking", id}
	}
	start, end := b.StartTime, b.EndTime
	if p.Start != nil {
		start = *p.Start
	}
	if p.End != nil {
		end = *p.End
	}
	if !end.After(start) {
		return Booking{}, &ValidationError{[]FieldError{{Field: "end_time", Message: "end_time must be after start_time"}}}
	}
	if err := b.canReschedule(); err != nil {
		return Booking{}, err
	}
	if !s.SlotFree(b.ResourceID, start, end, b.ID) {
		return Booking{}, &SlotConflictError{b.ResourceID, start, end}
	}
	b.StartTime, b.EndTime = start, end
	if p.Notes != nil {
		b.Notes = p.Notes
	}
	return *b, nil
}

func (s *State) transition(id string, apply func(*Booking) error) (Booking, error) {
	b, ok := s.Bookings[id]
	if !ok {
		return Booking{}, &NotFoundError{"booking", id}
	}
	if err := apply(b); err != nil {
		return Booking{}, err
	}
	return *b, nil
}

func (s *State) ConfirmBooking(id string) (Booking, error) {
	return s.transition(id, (*Booking).Confirm)
}

func (s *State) CancelBooking(id string, reason *string) (Booking, error) {
	return s.transition(id, func(b *Booking) error { return b.Cancel(reason) })
}

func (s *State) AddSubscription(url string, events []string, now time.Time) Subscription {
	var secret [12]byte
	if _, err := rand.Read(secret[:]); err != nil {
		panic(err)
	}
	sub := Subscription{
		ID: randomID("whs_"), URL: url, Events: events,
		Secret: "whsec_" + hex.EncodeToString(secret[:]), CreatedAt: now.UTC().Truncate(time.Microsecond),
	}
	s.Subscriptions = append(s.Subscriptions, sub)
	return sub
}
