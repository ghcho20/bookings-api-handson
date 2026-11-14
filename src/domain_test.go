package main

import (
	"errors"
	"testing"
	"time"
)

func tm(s string) time.Time { return mustTime(s) }

func newInput(resource, start, end string) BookingInput {
	return BookingInput{ResourceID: resource, Start: tm(start), End: tm(end),
		Customer: Customer{Name: "QA", Email: "qa@example.com"}}
}

func TestStateMachine(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		action  func(*Booking) error
		want    string // resulting status
		wantErr bool
	}{
		{"pending confirms", StatusPending, (*Booking).Confirm, StatusConfirmed, false},
		{"confirmed cannot confirm again", StatusConfirmed, (*Booking).Confirm, StatusConfirmed, true},
		{"cancelled cannot confirm", StatusCancelled, (*Booking).Confirm, StatusCancelled, true},
		{"completed cannot confirm", StatusCompleted, (*Booking).Confirm, StatusCompleted, true},
		{"pending cancels", StatusPending, func(b *Booking) error { return b.Cancel(nil) }, StatusCancelled, false},
		{"confirmed cancels", StatusConfirmed, func(b *Booking) error { return b.Cancel(nil) }, StatusCancelled, false},
		{"cancelled cannot cancel again", StatusCancelled, func(b *Booking) error { return b.Cancel(nil) }, StatusCancelled, true},
		{"completed cannot cancel", StatusCompleted, func(b *Booking) error { return b.Cancel(nil) }, StatusCompleted, true},
		{"confirmed completes", StatusConfirmed, (*Booking).Complete, StatusCompleted, false},
		{"pending cannot complete", StatusPending, (*Booking).Complete, StatusPending, true},
		{"cancelled cannot complete", StatusCancelled, (*Booking).Complete, StatusCancelled, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &Booking{ID: "bkg_x", Status: tc.from}
			err := tc.action(b)
			var te *TransitionError
			if tc.wantErr != errors.As(err, &te) {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if b.Status != tc.want {
				t.Errorf("status = %s, want %s", b.Status, tc.want)
			}
		})
	}
}

func TestCancelRecordsReason(t *testing.T) {
	b := &Booking{ID: "bkg_x", Status: StatusConfirmed}
	if err := b.Cancel(strPtr("moved")); err != nil {
		t.Fatal(err)
	}
	if b.CancelReason == nil || *b.CancelReason != "moved" {
		t.Errorf("cancel_reason = %v", b.CancelReason)
	}
}

func TestOverlapHalfOpen(t *testing.T) {
	h := func(n int) time.Time { return time.Date(2026, 12, 8, n, 0, 0, 0, time.UTC) }
	tests := []struct {
		name           string
		aS, aE, bS, bE time.Time
		want           bool
	}{
		{"identical", h(9), h(10), h(9), h(10), true},
		{"partial", h(9), h(11), h(10), h(12), true},
		{"contained", h(9), h(12), h(10), h(11), true},
		{"back to back after", h(9), h(10), h(10), h(11), false},
		{"back to back before", h(10), h(11), h(9), h(10), false},
		{"disjoint", h(9), h(10), h(13), h(14), false},
	}
	for _, tc := range tests {
		if got := overlaps(tc.aS, tc.aE, tc.bS, tc.bE); got != tc.want {
			t.Errorf("%s: overlaps = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOnlyPendingAndConfirmedOccupySlots(t *testing.T) {
	for _, status := range []string{StatusPending, StatusConfirmed, StatusCancelled, StatusCompleted} {
		st := seedState()
		st.Bookings = map[string]*Booking{"b1": {ID: "b1", ResourceID: "res_3f9a1c", Status: status,
			StartTime: tm("2026-12-08T09:00:00Z"), EndTime: tm("2026-12-08T10:00:00Z")}}
		free := st.SlotFree("res_3f9a1c", tm("2026-12-08T09:30:00Z"), tm("2026-12-08T10:30:00Z"), "")
		want := status == StatusCancelled || status == StatusCompleted
		if free != want {
			t.Errorf("status %s: SlotFree = %v, want %v", status, free, want)
		}
	}
}

func TestSlotFreeIsPerResource(t *testing.T) {
	st := seedState()
	if _, _, err := st.CreateBooking("key-00001", newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !st.SlotFree("res_8b44d0", tm("2026-12-08T09:00:00Z"), tm("2026-12-08T10:00:00Z"), "") {
		t.Error("a booking on one resource must not block another resource")
	}
}

func TestFreeSlotsBookingRemovesAndCancelGivesBack(t *testing.T) {
	st := seedState()
	from, to := tm("2026-12-07T09:00:00Z"), tm("2026-12-07T18:00:00Z")
	if got := st.FreeSlots("res_3f9a1c", from, to); len(got) != 1 || !got[0].StartTime.Equal(from) || !got[0].EndTime.Equal(to) {
		t.Fatalf("empty day: %+v", got)
	}
	b, _, err := st.CreateBooking("key-00001", newInput("res_3f9a1c", "2026-12-07T11:00:00Z", "2026-12-07T12:00:00Z"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := st.FreeSlots("res_3f9a1c", from, to)
	if len(got) != 2 || !got[0].EndTime.Equal(tm("2026-12-07T11:00:00Z")) || !got[1].StartTime.Equal(tm("2026-12-07T12:00:00Z")) {
		t.Fatalf("after booking: %+v", got)
	}
	if _, err := st.CancelBooking(b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := st.FreeSlots("res_3f9a1c", from, to); len(got) != 1 {
		t.Fatalf("after cancel the slot must come back: %+v", got)
	}
}

func TestFreeSlotsClipsBookingsToWindow(t *testing.T) {
	st := seedState()
	st.Bookings = map[string]*Booking{"b1": {ID: "b1", ResourceID: "res_3f9a1c", Status: StatusConfirmed,
		StartTime: tm("2026-12-07T08:00:00Z"), EndTime: tm("2026-12-07T10:00:00Z")}}
	got := st.FreeSlots("res_3f9a1c", tm("2026-12-07T09:00:00Z"), tm("2026-12-07T18:00:00Z"))
	if len(got) != 1 || !got[0].StartTime.Equal(tm("2026-12-07T10:00:00Z")) {
		t.Fatalf("got %+v", got)
	}
}

func TestCreateBookingConflicts(t *testing.T) {
	st := seedState()
	in := newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z")
	if _, _, err := st.CreateBooking("key-00001", in, time.Now()); err != nil {
		t.Fatal(err)
	}
	var slot *SlotConflictError
	if _, _, err := st.CreateBooking("key-00002", in, time.Now()); !errors.As(err, &slot) {
		t.Fatalf("same slot: err = %v, want SlotConflictError", err)
	}
	var nf *NotFoundError
	if _, _, err := st.CreateBooking("key-00003", newInput("res_nope", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z"), time.Now()); !errors.As(err, &nf) {
		t.Fatalf("unknown resource: err = %v, want NotFoundError", err)
	}
}

func TestIdempotency(t *testing.T) {
	st := seedState()
	in := newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z")
	first, replayed, err := st.CreateBooking("key-00001", in, time.Now())
	if err != nil || replayed {
		t.Fatalf("first: replayed=%v err=%v", replayed, err)
	}
	before := len(st.Bookings)

	again, replayed, err := st.CreateBooking("key-00001", in, time.Now())
	if err != nil || !replayed || again.ID != first.ID {
		t.Fatalf("same key and body must replay: id=%s replayed=%v err=%v", again.ID, replayed, err)
	}
	if len(st.Bookings) != before {
		t.Error("a replay must not create a booking")
	}

	other := newInput("res_3f9a1c", "2026-12-08T13:00:00Z", "2026-12-08T14:00:00Z")
	if _, _, err := st.CreateBooking("key-00001", other, time.Now()); !errors.Is(err, ErrIdempotencyReuse) {
		t.Fatalf("same key, different body: err = %v, want ErrIdempotencyReuse", err)
	}
}

func TestIdempotencyReplaysTheOriginalResponse(t *testing.T) {
	st := seedState()
	in := newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z")
	first, _, _ := st.CreateBooking("key-00001", in, time.Now())
	if _, err := st.ConfirmBooking(first.ID); err != nil {
		t.Fatal(err)
	}
	again, replayed, _ := st.CreateBooking("key-00001", in, time.Now())
	if !replayed || again.Status != StatusPending {
		t.Errorf("replay should return the booking as first returned (pending), got %s replayed=%v", again.Status, replayed)
	}
}

func TestIdempotencyIgnoresRepresentation(t *testing.T) {
	st := seedState()
	a := newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z")
	b := a
	b.Start = a.Start.In(time.FixedZone("KST", 9*3600)) // same instant, different offset
	b.End = a.End.In(time.FixedZone("KST", 9*3600))
	if _, _, err := st.CreateBooking("key-00001", a, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := st.CreateBooking("key-00001", b, time.Now()); err != nil || !replayed {
		t.Fatalf("same instants in another offset must replay: replayed=%v err=%v", replayed, err)
	}
}

func TestFailedCreateRecordsNoKey(t *testing.T) {
	st := seedState()
	bad := newInput("res_nope", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z")
	if _, _, err := st.CreateBooking("key-00001", bad, time.Now()); err == nil {
		t.Fatal("expected an error")
	}
	if _, ok := st.Idempotency["key-00001"]; ok {
		t.Error("a failed create must not record its key")
	}
}

func TestReschedule(t *testing.T) {
	st := seedState()
	start, end := tm("2026-12-09T14:00:00Z"), tm("2026-12-09T15:00:00Z")

	b, err := st.Reschedule("bkg_9d2e47", BookingPatch{Start: &start, End: &end})
	if err != nil || !b.StartTime.Equal(start) {
		t.Fatalf("reschedule: %+v err=%v", b, err)
	}
	// Moving onto its own slot is fine (ignored), onto another booking's is not.
	if _, _, err := st.CreateBooking("key-00001", newInput("res_3f9a1c", "2026-12-10T09:00:00Z", "2026-12-10T10:00:00Z"), time.Now()); err != nil {
		t.Fatal(err)
	}
	s2, e2 := tm("2026-12-10T09:30:00Z"), tm("2026-12-10T10:30:00Z")
	var slot *SlotConflictError
	if _, err := st.Reschedule("bkg_9d2e47", BookingPatch{Start: &s2, End: &e2}); !errors.As(err, &slot) {
		t.Fatalf("overlap: err = %v", err)
	}
	var tr *TransitionError
	if _, err := st.Reschedule("bkg_ca7c31", BookingPatch{Start: &start, End: &end}); !errors.As(err, &tr) {
		t.Fatalf("cancelled booking: err = %v", err)
	}
	var val *ValidationError
	badEnd := tm("2026-12-09T13:00:00Z")
	if _, err := st.Reschedule("bkg_9d2e47", BookingPatch{End: &badEnd}); !errors.As(err, &val) {
		t.Fatalf("end before start: err = %v", err)
	}
	var nf *NotFoundError
	if _, err := st.Reschedule("bkg_nope", BookingPatch{Start: &start, End: &end}); !errors.As(err, &nf) {
		t.Fatalf("unknown id: err = %v", err)
	}
}

func TestSeedMatchesTheMock(t *testing.T) {
	st := seedState()
	want := map[string]string{"bkg_9d2e47": StatusPending, "bkg_c0f1a2": StatusConfirmed, "bkg_ca7c31": StatusCancelled, "bkg_d0e5f4": StatusCompleted}
	if len(st.Bookings) != len(want) {
		t.Fatalf("seed has %d bookings", len(st.Bookings))
	}
	for id, status := range want {
		if b, ok := st.Bookings[id]; !ok || b.Status != status {
			t.Errorf("%s: %+v", id, b)
		}
	}
	if len(st.Resources) != 2 || st.FindResource("res_3f9a1c") == nil || st.FindResource("res_8b44d0") == nil {
		t.Errorf("resources: %+v", st.Resources)
	}
}
