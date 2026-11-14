Write a QA integration-test collection for the Bookings API, and update the local mock so the tests can run
against it. Read first: bookings-api.yaml (the contract), postman/mocks/bookings-api/default.js and
config.yaml (the mock), postman/collections/Reset Mock (a hand-written v3 collection to copy the format from).

Goal
- A hand-written collection that walks a booking through its whole lifecycle, plus a data-driven create
  test fed by a CSV, plus a "break it" demo showing the tests catch a regression.
- Hand-written means QA owns it. Do NOT edit the generated collections (Bookings API, [Smoke] Bookings API,
  [Contract] Bookings API): bootstrap overwrites them. Give the new collection a name that is none of
  those, for example "QA Integration Tests".

Where things go
- Collection: postman/collections/QA Integration Tests/ in Postman collection v3 YAML (a folder of
  .resources/definition.yaml and *.request.yaml files, like the other collections). Use the
  postman:collection-schema-v3 skill or the existing files for the exact shapes; scripts are
  `type: afterResponse` / `beforeRequest` with `language: text/javascript`.
- Collection-level settings: variable baseUrl (default http://localhost:4500/v1) and API key auth
  (X-API-Key: {{apiKey}}), exactly as Reset Mock does.
- Environment: the mock runs on the existing `mock` environment (postman/environments/mock.environment.yaml).
  Run every collection with `-e postman/environments/mock.environment.yaml`. Do not edit that file and do
  not add another environment. Its baseUrl is empty and it has no apiKey, so pass both with --env-var.
- CSV: qa/qa-create-booking.csv (create the qa/ folder). It lives outside postman/, so Publish does not push
  it to the workspace; it is only the input of `postman collection run -d`.
- Mock: edit postman/mocks/bookings-api/default.js (and config.yaml only if needed).
- Publishing: the Publish workflow pushes postman/** to the cloud workspace after the change is committed to
  main. Do NOT run `postman workspace push`, deploy the mock, or commit/push anything yourself.

Part 1: lifecycle chain (folder "Lifecycle")
Requests, in this order. Pass ids between them with collection variables set in afterResponse scripts, and
assert status code, body fields and the content type at every step.
1. Find a free slot: GET /resources/res_3f9a1c/availability for a fixed window in December 2026
   (for example 2026-12-07T09:00:00Z to 18:00:00Z; the spec allows windows up to 14 days). Assert 200 and a
   non-empty slots list. Store the first hour of the first slot as slot_start and slot_end.
2. Create: POST /bookings with a fresh Idempotency-Key ({{$guid}}) and the stored slot. Assert 201, status
   "pending", a Location header; store booking_id.
3. Get: GET /bookings/{{booking_id}}. Assert 200, same id, status "pending".
4. Confirm: POST /bookings/{{booking_id}}/confirm. Assert 200 and status "confirmed".
5. Cancel: POST /bookings/{{booking_id}}/cancel with a reason. Assert 200, status "cancelled", reason echoed.
6. Cancel again: same request. Assert 409 with application/problem+json and a type ending in
   "invalid-transition".
The chain must be safe to run alone and must not touch the seeded booking bkg_9d2e47 (Smoke and Contract use it).

Part 2: data-driven create (folder "Create booking (data-driven)")
- One POST /bookings request. Every value comes from a CSV column, including the API key: give this request
  its own X-API-Key auth with value {{api_key}} (not {{apiKey}}), so a row can send no key. The Idempotency-Key
  is {{$guid}} so each iteration gets its own key (the mock replays a repeated key before it validates, which
  would hide an invalid row).
- CSV columns: case_name,api_key,resource_id,start_time,end_time,customer_name,customer_email,expected_status
- Rows, in this order (order matters: the 409 row needs the 201 row's booking to exist). Use res_8b44d0 and
  December 2026 slots, away from the seeded and the lifecycle bookings. api_key is demo-key unless noted.
    1. valid booking      -> 2026-12-08 09:00-10:00Z, expected_status 201
    2. unknown resource   -> resource_id res_does_not_exist, expected_status 404
    3. end before start   -> end_time earlier than start_time, expected_status 422
    4. slot taken         -> the same resource and 09:00-10:00Z slot as row 1, expected_status 409
    5. missing API key    -> api_key empty, otherwise a valid 11:00-12:00Z request, expected_status 401
    6. no key, bad body   -> api_key empty AND end before start, expected_status 401
                             (authentication is checked before validation, so this is 401, not 422)
- The test script compares the response code with expected_status.
  - 201: status "pending" and a booking id.
  - 404, 409, 422: application/problem+json and body.status equal to expected_status; for 409 also a type
    ending in "slot-taken"; for 422 a non-empty errors list naming end_time.
  - 401: application/problem+json, body.status 401, and a WWW-Authenticate header.
- Run it with `-d qa/qa-create-booking.csv -i "Create booking (data-driven)"` so the lifecycle folder is
  not repeated for every row.

Part 3: update the mock
The mock is already stateful (pm.state, seeded on first use) and implements the lifecycle, 404, 409 and 422.
Run the QA collection against it first and fix only what fails or is missing. Known requirements:
- Add a break-it switch for the demo: when set, POST /bookings/{id}/confirm returns 200 but leaves the booking
  "pending" (a silent bug). Off by default. Implement it as one clearly commented constant at the top of
  default.js, driven by `process.env.MOCK_BREAK_CONFIRM === '1'`, and document it in the file header. If
  `postman mock run` does not pass the environment variable through, use a plain `const` that is edited by
  hand and say so in the header.
- Auth: the mock must keep treating a missing or empty X-API-Key (and no Bearer token) as 401 with a
  WWW-Authenticate header, and must do it before body validation. It already does; keep it that way.
- Keep it deployable: Node built-ins only, no local requires. Keep the `// @endpoint` annotation on every spec
  route and none on mock-only routes. `postman mock get postman/mocks/bookings-api` must still list exactly the
  10 spec operations.
- Do not run `postman mock generate --update` (it overwrites the hand edits).
- State persists until reset and can survive a restart. Run "Reset Mock" before every run of the QA
  collection, the CSV run, Smoke and Contract. Say in the collection description that this is required.

Verify (report the exact commands and results)
Use: -e postman/environments/mock.environment.yaml --env-var "baseUrl=http://localhost:4500/v1"
--env-var "apiKey=demo-key" --no-report-events
1. Start the mock: postman mock run postman/mocks/bookings-api --port 4500 (restart after every edit).
2. Run "Reset Mock", then the Lifecycle folder with --bail: all assertions pass.
3. Run "Reset Mock", then the CSV run: six iterations (201, 404, 422, 409, 401, 401), all pass. Show the
   expected_status of each iteration next to the actual status.
4. Run "Reset Mock", then the whole QA collection once more to confirm it still passes after the CSV run's
   state, and note anything that makes it order-dependent.
5. Regression: "Reset Mock", then "[Smoke] Bookings API" (all pass) and "[Contract] Bookings API" with
   -i resources -i bookings -i webhook-subscriptions (same result as before your mock edit).
6. Break-it demo: restart the mock with MOCK_BREAK_CONFIRM=1, run "Reset Mock", rerun the Lifecycle folder
   with --bail. Show that step 4 fails and the exit code is 1. Then restart without the switch and show
   the exit code is 0 again.
7. `postman workspace diff --push-strategy default` shows the new collection as a create and nothing else new.
Say plainly what you ran, what you did not, and what you assumed.
