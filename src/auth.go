package main

import (
	"crypto/subtle"
	"net/http"
)

type accessLevel int

const (
	accessNone accessLevel = iota
	accessReadOnly
	accessFull
)

// Authenticator maps an X-API-Key to an access level. OAuth2 bearer tokens are
// not implemented: a request without a valid X-API-Key is unauthenticated.
type Authenticator struct {
	full     []string
	readOnly []string
}

func NewAuthenticator(cfg Config) *Authenticator {
	return &Authenticator{full: cfg.APIKeys, readOnly: cfg.ReadOnlyKeys}
}

// matches compares against every key, so timing does not reveal which one.
func matches(keys []string, got string) bool {
	found := 0
	for _, k := range keys {
		found |= subtle.ConstantTimeCompare([]byte(k), []byte(got))
	}
	return found == 1
}

func (a *Authenticator) Level(r *http.Request) accessLevel {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		return accessNone
	}
	switch {
	case matches(a.full, key):
		return accessFull
	case matches(a.readOnly, key):
		return accessReadOnly
	}
	return accessNone
}

// guard wraps an operation: unknown or missing key is 401, a read-only key on a
// write is 403. Both are decided before the handler reads or validates the body.
func (a *Authenticator) guard(write bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch a.Level(r) {
		case accessNone:
			unauthorized(w)
		case accessReadOnly:
			if write {
				forbidden(w)
				return
			}
			next(w, r)
		default:
			next(w, r)
		}
	}
}
