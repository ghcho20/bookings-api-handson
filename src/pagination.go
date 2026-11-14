package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

// Page is the list response shape: a page of items and the cursor for the next one.
type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// parseLimit reads ?limit= (1 to 100, default 20).
func parseLimit(q url.Values) (int, error) {
	raw := q.Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		return 0, errors.New("Query parameter 'limit' must be an integer from 1 to 100.")
	}
	return n, nil
}

// The cursor is opaque to clients: base64url of {"o":<offset>}. The offset form
// is what the collections' example cursor (eyJvIjoyMH0 = {"o":20}) already uses.
func encodeCursor(offset int) string {
	b, _ := json.Marshal(struct {
		O int `json:"o"`
	}{offset})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(c string) (int, error) {
	bad := errors.New("Query parameter 'cursor' is not a valid cursor.")
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, bad
	}
	var v struct {
		O *int `json:"o"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.O == nil || *v.O < 0 {
		return 0, bad
	}
	return *v.O, nil
}

// paginate returns the page of items starting at offset and the next cursor
// (nil on the last page). The page is never nil, so it renders as [].
func paginate[T any](items []T, limit, offset int) ([]T, *string) {
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]T{}, items[offset:end]...)
	if end >= len(items) {
		return page, nil
	}
	next := encodeCursor(end)
	return page, &next
}

// pageParams reads limit and cursor from a query string.
func pageParams(q url.Values) (limit, offset int, err error) {
	if limit, err = parseLimit(q); err != nil {
		return
	}
	offset, err = decodeCursor(q.Get("cursor"))
	return
}
