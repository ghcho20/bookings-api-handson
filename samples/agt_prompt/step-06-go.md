Build the real Bookings API backend in Go, as a container you can run locally with Docker Compose, and make it
pass every test collection in this repo. These tests are the CI/CD gate that step 07 will wire into GitHub
Actions, so what passes here must pass there, from one command.

Read first: bookings-api.yaml (the contract; it wins over everything else), postman/mocks/bookings-api/default.js
(a stateful mock of the same API: the behavioral reference, including the seed data), and the collections under
postman/collections/ and qa/.

Requirements
- Go, standard library only (net/http with Go 1.22+ pattern routing, encoding/json, sync, os). No third-party
  modules, no database.
- Persistence: one JSON file (default /data/state.json) written atomically (temp file, fsync, rename) on every
  change, loaded at startup, and seeded when the file is missing. A single mutex guards all reads and writes of
  the state, so concurrent requests cannot double-book a slot.
- A container run by Docker Compose: `docker compose up --build --wait` brings it up healthy.
- It must pass all the test collections listed under "The gate" below, unchanged.

Layout (create these; src/ does not exist yet)
- src/go.mod, src/main.go and a few small files or packages (handlers, store, validation, problem responses).
  Unit tests (`go test ./...`) for the booking state machine, slot overlap, pagination and idempotency.
- src/Dockerfile: multi-stage; static binary (CGO_ENABLED=0); small non-root final image; pin the Go version to
  the go.mod directive. Add src/.dockerignore.
- docker-compose.yml at the repo root: one service `bookings-api`, build ./src, published port 8080 (the mock
  keeps 4500, so both can run side by side), a named volume mounted at /data, a healthcheck on GET /healthz,
  and environment: PORT, DATA_FILE, API_KEYS, API_KEYS_READONLY, ENABLE_TEST_ROUTES. Compose defaults:
  API_KEYS=demo-key, API_KEYS_READONLY=ro_demo-key, ENABLE_TEST_ROUTES=true. This is a test server, so these
  hardcoded defaults are fine. They must be only compose defaults overridable from the environment; the Go
  code must not contain the literal keys. The compose file stays at the repo root (not in src/): it is the
  entry point developers and CI run. Everything else about the backend is under src/.
- scripts/run-collections.sh: the gate in one script (see below). Step 07's workflow should only call it.
- A "Notes for step 07" comment block at the top of docker-compose.yml and of scripts/run-collections.sh,
  stating: the compose file is at the repo root, not in src/; the gate is `scripts/run-collections.sh`
  (which runs `docker compose up --build --wait` from the root and tears it down); the working keys are
  demo-key (full access) and ro_demo-key (read-only); the gate runs with ENABLE_TEST_ROUTES=true; the
  Postman collections run with the `local` environment and `--no-report-events`; and the CI workflow should
  trigger on changes to src/**, docker-compose.yml, scripts/**, qa/** and postman/**, never on the
  generated-collection refresh alone. Keep the note short and accurate.
- .gitignore entries for any local data files. Do not commit state.json.
- postman/environments/local.environment.yaml: baseUrl http://localhost:8080/v1 and an `apiKey` variable that is
  secret with no value (supplied with --env-var). Use the postman:variables and postman:collection-schema-v3
  skills for the secret declaration. Do not edit the existing `mock` environment.

Behavior to implement (all per the spec; the mock shows the shapes)
- Routes: the 10 operations under /v1 (resources, availability, bookings CRUD-ish, confirm, cancel,
  webhook-subscriptions). Add GET /healthz (no auth, outside /v1). Unknown route 404 and wrong method 405 with
  Allow, both application/problem+json.
- Auth: X-API-Key must be one of API_KEYS (full access) or API_KEYS_READONLY (reads only; writes return 403).
  A missing or unknown key returns 401 with application/problem+json and a WWW-Authenticate header, checked
  before body validation. OAuth2 bearer tokens are not implemented: a Bearer token is 401.
- Validation: 400 for bad JSON, a missing or malformed Idempotency-Key (8 to 64 characters), or bad query
  parameters; 422 with an `errors` list of {field, message} for invalid fields (end before start, bad email,
  notes over 1000 characters, and so on). Timestamps are RFC 3339 and stored as UTC.
- Bookings: pending -> confirmed -> completed, and cancel from pending or confirmed. Illegal transitions return
  409 (a type ending in `invalid-transition`). Creating or rescheduling into an occupied slot returns 409
  (`slot-taken`); only pending and confirmed bookings occupy a slot, half-open [start, end).
- Idempotency: the same Idempotency-Key with the same body returns the original booking with 201 and
  `Idempotent-Replayed: true`. The same key with a different body must not silently replay: follow what the spec
  says, or return 409 and note the choice.
- Availability: computed from real bookings; reject windows over 14 days as the spec says. A booking removes its
  slot, and cancelling gives it back.
- Lists: filters, `limit` 1 to 100 (default 20), opaque cursor, newest first.
- Webhook subscriptions: validate (HTTPS URL, known events), store, return the secret once. Delivery is NOT
  implemented.
- Not implemented, and you must say so in your report: OAuth2 bearer validation, 429 rate limiting (so no
  Retry-After or RateLimit headers), webhook delivery, and the mock's x-mock-scenario header.
- Problem type URIs, field names and header names: take them from the spec; use the mock's values where the spec
  is silent (https://api.bookings.example.com/problems/<slug>).

Test-only support (so the existing collections work against the backend unchanged)
- Seed data: the same ids and values as the mock's SEED_RESOURCES and seedBookings (res_3f9a1c, res_8b44d0,
  bkg_9d2e47 pending, and the other three bookings). Copy them exactly; the collections depend on them.
- POST /v1/__mock/reset: re-seeds the file and returns {"reset": true, "bookings": [<seeded ids>]}, the shape the
  "Reset Mock" collection asserts. It exists only when ENABLE_TEST_ROUTES=true; otherwise it is a plain 404, and
  it needs no API key (like the mock). Name it as the collection expects, even though this is not a mock.
- Do not add the mock's /__mock/bookings/{id}/complete route (no collection uses it).

The gate (what must pass; scripts/run-collections.sh runs it in this order and exits non-zero on the first failure)
Common flags: -e postman/environments/local.environment.yaml --env-var "baseUrl=http://localhost:8080/v1"
--env-var "apiKey=demo-key" --no-report-events (the key must be demo-key: the QA CSV hardcodes it)
1. "Reset Mock"
2. "[Smoke] Bookings API" with -i resources -i bookings -i webhook-subscriptions
3. "[Contract] Bookings API" with the same -i flags
4. "QA Integration Tests", Lifecycle folder, with --bail
5. "QA Integration Tests", "Create booking (data-driven)" folder, with -d qa/qa-create-booking.csv (six rows)
Run "Reset Mock" again before steps 2, 4 and 5 (they change state). The same script starts the stack with
`docker compose up --build --wait` and stops it afterwards, also when a step fails, and takes baseUrl and apiKey as
optional arguments so CI can reuse it.
The generated Webhooks folder is skipped on purpose: it builds broken URLs (/v1booking.confirmed). Do not
implement routes for them. Say this in your report.

How to work
- Do not edit the generated collections (Bookings API, [Smoke], [Contract]); bootstrap overwrites them. If one of
  them fails for a reason that looks like a contract or test defect, the fix belongs in bookings-api.yaml:
  stop, explain the finding, and ask before changing the spec. You may fix the hand-written collections (QA
  Integration Tests, Reset Mock) if they are wrong, and say what you changed.
- First get a baseline: run the same collections against the mock (postman mock run postman/mocks/bookings-api
  --port 4500, with -e postman/environments/mock.environment.yaml and baseUrl http://localhost:4500/v1) and
  record the result. Then run them against the backend and compare. Fix the backend whenever it differs from the
  spec or the mock without a reason.
- Do not commit, push, tag or deploy. Do not run `postman workspace push`: the Publish workflow pushes
  postman/** (the new environment) after you commit.

Verify (run these and report the exact commands and results)
1. `cd src && go vet ./... && go test ./...` pass.
2. `docker compose up --build --wait` reaches healthy; `curl localhost:8080/healthz` returns 200.
3. The gate: `scripts/run-collections.sh` exits 0; report the request and assertion counts per collection, and
   the baseline from the mock next to them.
4. Persistence: create a booking, `docker compose restart`, get it back. `docker compose down` then `up` keeps it;
   `docker compose down -v` then `up` shows only the seed data.
5. Concurrency: send 20 parallel POST /v1/bookings for the same slot with different Idempotency-Keys; exactly one
   returns 201 and the rest 409.
6. With ENABLE_TEST_ROUTES unset, POST /v1/__mock/reset returns 404. A key from API_KEYS_READONLY gets 403 on a
   write and 200 on a read.
Say plainly what you ran, what you did not, what is not implemented, and what you assumed.
