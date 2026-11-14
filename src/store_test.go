package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "state.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestOpenSeedsMissingFile(t *testing.T) {
	s, path := openTemp(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("seeding must write the file: %v", err)
	}
	s.View(func(st *State) {
		if len(st.Bookings) != 4 {
			t.Errorf("seeded %d bookings", len(st.Bookings))
		}
	})
}

func createVia(s *Store, key string, in BookingInput) (Booking, error) {
	var out Booking
	err := s.Update(func(st *State) (bool, error) {
		b, rep, err := st.CreateBooking(key, in, time.Now())
		out = b
		return !rep && err == nil, err
	})
	return out, err
}

func TestChangesSurviveReopen(t *testing.T) {
	s, path := openTemp(t)
	b, err := createVia(s, "key-00001", newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.View(func(st *State) {
		got, ok := st.Bookings[b.ID]
		if !ok || got.Status != StatusPending || !got.StartTime.Equal(b.StartTime) {
			t.Errorf("reloaded booking: %+v", got)
		}
		if _, ok := st.Idempotency["key-00001"]; !ok {
			t.Error("idempotency record must persist")
		}
	})
}

func TestNoTempFilesLeftBehind(t *testing.T) {
	s, path := openTemp(t)
	for i := 0; i < 5; i++ {
		if err := s.Update(func(st *State) (bool, error) { return true, nil }); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("directory has %d entries: %v", len(entries), entries)
	}
}

func TestCorruptFileIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("expected an error for a corrupt state file")
	}
	if data, _ := os.ReadFile(path); string(data) != "{not json" {
		t.Error("a corrupt file must be left alone")
	}
}

func TestUpdateErrorRollsBack(t *testing.T) {
	s, _ := openTemp(t)
	boom := errors.New("boom")
	err := s.Update(func(st *State) (bool, error) {
		st.Bookings["bkg_9d2e47"].Status = StatusCancelled // mutate, then fail
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	s.View(func(st *State) {
		if st.Bookings["bkg_9d2e47"].Status != StatusPending {
			t.Error("a failed update must leave the state unchanged")
		}
	})
}

func TestPersistFailureRollsBack(t *testing.T) {
	s, path := openTemp(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil { // read-only: temp file cannot be created
		t.Skip("cannot make directory read-only")
	}
	defer os.Chmod(dir, 0o700)
	if f, err := os.CreateTemp(dir, "probe"); err == nil { // running as root: permissions are not enforced
		f.Close()
		t.Skip("directory permissions are not enforced here")
	}
	_, err := createVia(s, "key-00001", newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z"))
	var pe *PersistError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want PersistError", err)
	}
	s.View(func(st *State) {
		if len(st.Bookings) != 4 || len(st.Idempotency) != 0 {
			t.Error("a booking that could not be saved must not stay in memory")
		}
	})
}

func TestResetReseeds(t *testing.T) {
	s, path := openTemp(t)
	if _, err := createVia(s, "key-00001", newInput("res_3f9a1c", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	ids, err := s.Reset()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bkg_9d2e47", "bkg_c0f1a2", "bkg_ca7c31", "bkg_d0e5f4"}
	if len(ids) != 4 {
		t.Fatalf("ids = %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids = %v, want %v", ids, want)
		}
	}
	reopened, _ := OpenStore(path)
	reopened.View(func(st *State) {
		if len(st.Bookings) != 4 || len(st.Idempotency) != 0 {
			t.Errorf("reset must reach the file: %d bookings, %d keys", len(st.Bookings), len(st.Idempotency))
		}
	})
}

// Twenty requests race for one slot with different keys: the lock makes
// exactly one of them win.
func TestConcurrentCreatesCannotDoubleBook(t *testing.T) {
	s, _ := openTemp(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, conflicts := 0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := createVia(s, "race-key-"+itoa(i), newInput("res_8b44d0", "2026-12-08T09:00:00Z", "2026-12-08T10:00:00Z"))
			mu.Lock()
			defer mu.Unlock()
			var slot *SlotConflictError
			switch {
			case err == nil:
				created++
			case errors.As(err, &slot):
				conflicts++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if created != 1 || conflicts != 19 {
		t.Fatalf("created=%d conflicts=%d, want 1 and 19", created, conflicts)
	}
}
