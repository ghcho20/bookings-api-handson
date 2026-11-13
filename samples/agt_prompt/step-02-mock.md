Add a mock server for the Bookings API, listening on port 4500.

Source and layout
- One mock at postman/mocks/bookings-api/ (config.yaml + default.js), generated from bookings-api.yaml
  with `postman mock generate`. It serves the spec's base path: /v1/...
- Target collections in ./postman/collections:
  - Bookings API
  - [Smoke] Bookings API
  - [Contract] Bookings API
  All three are generated from the same spec and share the same requests, so one mock covers them.
  Do not edit these collections (bootstrap overwrites them); use them only to test the mock.
- Add a hand-written "Reset Mock" collection at ./postman/collections/Reset Mock (v3 YAML, like the
  others). It calls the mock's reset route, then checks that the booking the collections act on
  (bkg_9d2e47) is back to pending. It is not generated from the spec, so bootstrap leaves it alone;
  the Publish workflow pushes it with the rest.

Behavior (stateful)
- Keep bookings in pm.state and seed them on first use. Support the lifecycle
  pending -> confirmed -> completed and cancel; return 409 on illegal transitions (for example
  confirming a cancelled booking) and 404 for an unknown id.
- POST /bookings requires an Idempotency-Key header; a repeated key returns the original booking.
- Add a reset route so a test can start from the seed.
- Cover the documented errors: 400 (availability), 401, 403, 404, 409, 422, 429 (with Retry-After), 500.
- Scenarios selectable with x-mock-scenario: rate-limited (429), server-error (500).

Auth
- Return 401 (application/problem+json, with the documented WWW-Authenticate header) when X-API-Key
  is missing. The Smoke and Contract runs must send a key.

Config requirements
- config.yaml: interceptRequests: true.
- The handler uses require() (CommonJS) and listens on process.env.PORT.
- Run with --port 4500 (a busy port moves otherwise). Take the URL from the line it prints, and
  restart after every edit.
- Write the handler so it would also work on a deployed mock server: Node built-ins only, no local
  requires.

Endpoint annotations
- `postman mock generate` writes a `// @endpoint METHOD /path` comment above each route, and
  `postman mock get` lists the mock's endpoints from them. Keep them as part of building the
  handler: when you rewrite or add a route, write its annotation in the same edit. Don't leave
  them for a later pass.
- Put one line per method directly above the route's branch. Cover the spec's operations only,
  using the spec's base path and the generator's `:param` style with the spec's parameter names
  (e.g. `GET /v1/bookings/:booking_id`).
- Don't annotate mock-only routes (reset) or the webhook receivers; they still work, just unlisted.

Verify
- `postman mock get postman/mocks/bookings-api` lists exactly the 10 spec operations in every
  scenario, with no duplicates.
- Start it: postman mock run postman/mocks/bookings-api --port 4500
- Run "Reset Mock" (same --env-var values) before every Smoke or Contract run. Those runs reschedule,
  confirm and cancel bkg_9d2e47, so a second run without a reset gets three 409s. State can survive a
  mock restart (for example when the Postman app runs the mock), so a restart is not a reset.
- Run "[Smoke] Bookings API" against it with --env-var "baseUrl=http://localhost:4500/v1" and the API
  key; all requests pass. Run "[Contract] Bookings API" only with
  -i resources -i bookings -i webhook-subscriptions (the generated webhook requests have a broken URL).
- Show: a documented error comes back, a scenario changes the response, and a POST then a GET shows
  the stored booking.

Where it runs
- Run locally only. Do not push, deploy, or use --public. The Publish workflow pushes the mock to the
  workspace after it is committed to main.
- If the spec changes, note that `postman mock generate --update` overwrites handler edits.
