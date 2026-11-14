package main

import "time"

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func strPtr(s string) *string { return &s }

// seedState returns the initial data. The ids and values are the same as the
// mock's SEED_RESOURCES / seedBookings: the Postman collections depend on them
// (bkg_9d2e47 must start pending).
func seedState() *State {
	jisoo := Customer{Name: "Jisoo Kim", Email: "jisoo.kim@example.com"}
	bookings := []*Booking{
		{ID: "bkg_9d2e47", ResourceID: "res_3f9a1c", Status: StatusPending,
			StartTime: mustTime("2026-11-10T11:00:00Z"), EndTime: mustTime("2026-11-10T12:00:00Z"),
			Customer: jisoo, Notes: strPtr("Quarterly planning"), CreatedAt: mustTime("2026-11-03T06:00:00Z")},
		{ID: "bkg_c0f1a2", ResourceID: "res_8b44d0", Status: StatusConfirmed,
			StartTime: mustTime("2026-11-11T09:00:00Z"), EndTime: mustTime("2026-11-11T10:00:00Z"),
			Customer: Customer{Name: "Minho Lee", Email: "minho.lee@example.com"}, CreatedAt: mustTime("2026-11-02T08:00:00Z")},
		{ID: "bkg_ca7c31", ResourceID: "res_3f9a1c", Status: StatusCancelled,
			StartTime: mustTime("2026-11-12T09:00:00Z"), EndTime: mustTime("2026-11-12T10:00:00Z"),
			Customer: jisoo, CancelReason: strPtr("Meeting moved to next week"), CreatedAt: mustTime("2026-11-01T08:00:00Z")},
		{ID: "bkg_d0e5f4", ResourceID: "res_3f9a1c", Status: StatusCompleted,
			StartTime: mustTime("2026-11-05T09:00:00Z"), EndTime: mustTime("2026-11-05T10:00:00Z"),
			Customer: jisoo, Notes: strPtr("Retro"), CreatedAt: mustTime("2026-10-30T08:00:00Z")},
	}
	s := &State{
		Version: 1,
		Resources: []Resource{
			{ID: "res_3f9a1c", Name: "Han River Room", Type: "meeting_room", Location: "seoul-hq", Capacity: 8},
			{ID: "res_8b44d0", Name: "Namsan Room", Type: "meeting_room", Location: "seoul-hq", Capacity: 4},
		},
		Bookings:      map[string]*Booking{},
		Idempotency:   map[string]IdemRecord{},
		Subscriptions: []Subscription{},
	}
	for _, b := range bookings {
		s.Bookings[b.ID] = b
	}
	return s
}
