package main

import (
	"net/url"
	"testing"
)

func TestParseLimit(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", 20, false}, {"1", 1, false}, {"100", 100, false}, {"20", 20, false},
		{"0", 0, true}, {"101", 0, true}, {"-1", 0, true}, {"abc", 0, true}, {"1.5", 0, true},
	}
	for _, tc := range tests {
		got, err := parseLimit(url.Values{"limit": {tc.raw}})
		if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.want) {
			t.Errorf("limit %q: got %d, err %v", tc.raw, got, err)
		}
	}
}

func TestCursorRoundTripAndCollectionExample(t *testing.T) {
	for _, off := range []int{0, 1, 20, 12345} {
		got, err := decodeCursor(encodeCursor(off))
		if err != nil || got != off {
			t.Errorf("offset %d: got %d err %v", off, got, err)
		}
	}
	// The generated collections send this cursor: {"o":20}.
	if got, err := decodeCursor("eyJvIjoyMH0"); err != nil || got != 20 {
		t.Errorf("collection cursor: got %d err %v", got, err)
	}
}

func TestInvalidCursors(t *testing.T) {
	for _, c := range []string{"!!!", "bm90LWpzb24", "e30", "eyJvIjotMX0"} { // not base64, not JSON, no o, o=-1
		if _, err := decodeCursor(c); err == nil {
			t.Errorf("cursor %q should be rejected", c)
		}
	}
}

func TestPaginateWalksEveryItemOnce(t *testing.T) {
	items := make([]int, 45)
	for i := range items {
		items[i] = i
	}
	var seen []int
	offset, pages := 0, 0
	for {
		page, next := paginate(items, 20, offset)
		seen = append(seen, page...)
		pages++
		if next == nil {
			break
		}
		var err error
		if offset, err = decodeCursor(*next); err != nil {
			t.Fatal(err)
		}
	}
	if pages != 3 || len(seen) != 45 {
		t.Fatalf("pages=%d seen=%d", pages, len(seen))
	}
	for i, v := range seen {
		if v != i {
			t.Fatalf("order or duplicate at %d: %d", i, v)
		}
	}
}

func TestPaginateEdges(t *testing.T) {
	items := []int{1, 2, 3}
	if page, next := paginate(items, 3, 0); len(page) != 3 || next != nil {
		t.Errorf("exact fit: %v %v", page, next)
	}
	page, next := paginate(items, 20, 20) // offset past the end, like the collections' cursor
	if page == nil || len(page) != 0 || next != nil {
		t.Errorf("past the end must be an empty non-nil page: %#v %v", page, next)
	}
	if page, next := paginate([]int(nil), 20, 0); page == nil || next != nil {
		t.Errorf("empty list: %#v %v", page, next)
	}
}
