/**
 * Bookings API mock (stateful). Generated from bookings-api.yaml with
 * `postman mock generate`, then hand-edited. Running
 * `postman mock generate --update` OVERWRITES these edits.
 *
 * Serves the spec's base path: /v1/...
 * State lives in pm.state and is seeded on first use.
 *
 * Mock-only routes (no auth):
 *   POST /v1/__mock/reset                     re-seed state
 *   POST /v1/__mock/bookings/{id}/complete    confirmed -> completed
 *
 * Scenarios (header x-mock-scenario): rate-limited (429), server-error (500)
 * Auth: X-API-Key or Authorization: Bearer <token> required (401 otherwise).
 *       A key starting with "ro_" is read-only: writes return 403.
 *       Auth is checked before any body validation.
 *
 * Break-it switch (QA regression demo), off by default:
 *   MOCK_BREAK_CONFIRM=1 postman mock run postman/mocks/bookings-api --port 4500
 *   makes POST /v1/bookings/{id}/confirm answer 200 but leave the booking
 *   "pending" (a silent bug the QA Integration Tests Lifecycle folder catches).
 *   See BREAK_CONFIRM below. Restart the mock to turn it on or off.
 *
 * Endpoint annotations (// @endpoint) cover the 10 spec operations only;
 * mock-only routes and the webhook receivers are deliberately unannotated.
 *
 * Node built-ins only, no local requires, so it also runs as a single file
 * on a deployed mock server.
 */
const http = require('http');
const crypto = require('crypto');

const PORT = process.env.PORT || 4500;
const BASE = '/v1';
const PROBLEM_BASE = 'https://api.bookings.example.com/problems/';

// BREAK-IT SWITCH (QA demo only). When true, confirm returns 200 with the
// booking still "pending". Off unless MOCK_BREAK_CONFIRM=1 is set when the mock
// starts; never set on a deployed mock server.
const BREAK_CONFIRM = process.env.MOCK_BREAK_CONFIRM === '1';

// ---------------------------------------------------------------- seed data

const SEED_RESOURCES = [
  { id: 'res_3f9a1c', name: 'Han River Room', type: 'meeting_room', location: 'seoul-hq', capacity: 8 },
  { id: 'res_8b44d0', name: 'Namsan Room', type: 'meeting_room', location: 'seoul-hq', capacity: 4 },
];

function seedBookings() {
  const customer = { name: 'Jisoo Kim', email: 'jisoo.kim@example.com' };
  const list = [
    // The collections act on bkg_9d2e47 (get -> reschedule -> confirm -> cancel),
    // so it starts pending, in a slot clear of the collections' 09-10 and 14-15 requests.
    { id: 'bkg_9d2e47', resource_id: 'res_3f9a1c', status: 'pending',
      start_time: '2026-11-10T11:00:00Z', end_time: '2026-11-10T12:00:00Z',
      customer, notes: 'Quarterly planning', cancel_reason: null, created_at: '2026-11-03T06:00:00Z' },
    { id: 'bkg_c0f1a2', resource_id: 'res_8b44d0', status: 'confirmed',
      start_time: '2026-11-11T09:00:00Z', end_time: '2026-11-11T10:00:00Z',
      customer: { name: 'Minho Lee', email: 'minho.lee@example.com' }, notes: null, cancel_reason: null,
      created_at: '2026-11-02T08:00:00Z' },
    { id: 'bkg_ca7c31', resource_id: 'res_3f9a1c', status: 'cancelled',
      start_time: '2026-11-12T09:00:00Z', end_time: '2026-11-12T10:00:00Z',
      customer, notes: null, cancel_reason: 'Meeting moved to next week', created_at: '2026-11-01T08:00:00Z' },
    { id: 'bkg_d0e5f4', resource_id: 'res_3f9a1c', status: 'completed',
      start_time: '2026-11-05T09:00:00Z', end_time: '2026-11-05T10:00:00Z',
      customer, notes: 'Retro', cancel_reason: null, created_at: '2026-10-30T08:00:00Z' },
  ];
  const map = {};
  for (const b of list) map[b.id] = b;
  return map;
}

async function seed() {
  await pm.state.set('resources', SEED_RESOURCES);
  await pm.state.set('bookings', seedBookings());
  await pm.state.set('idempotency', {});
  await pm.state.set('subscriptions', {});
  await pm.state.set('seeded', true);
}

async function ensureSeeded() {
  if (!(await pm.state.get('seeded'))) await seed();
}

// ---------------------------------------------------------------- helpers

function send(res, status, body, headers) {
  const payload = body === undefined ? '' : JSON.stringify(body);
  res.writeHead(status, Object.assign({
    'Content-Type': 'application/json',
    'Content-Length': Buffer.byteLength(payload),
  }, headers || {}));
  res.end(payload);
}

function problem(res, status, slug, title, detail, extra, headers) {
  const body = Object.assign({ type: PROBLEM_BASE + slug, title, status }, detail ? { detail } : {}, extra || {});
  send(res, status, body, Object.assign({ 'Content-Type': 'application/problem+json' }, headers || {}));
}

const unauthorized = (res) => problem(res, 401, 'unauthorized', 'Unauthorized',
  'A valid X-API-Key header or bearer token is required.', null, { 'WWW-Authenticate': 'Bearer realm="bookings-api"' });
const forbidden = (res) => problem(res, 403, 'insufficient-scope', 'Insufficient scope',
  "This operation requires the 'bookings:write' scope.");
const notFound = (res, what, id) => problem(res, 404, 'not-found', 'Not found', `No ${what} with id ${id}.`);
const invalidTransition = (res, b, action) => problem(res, 409, 'invalid-transition', 'Invalid status transition',
  `Booking ${b.id} is ${b.status} and cannot be ${action}.`);
const slotTaken = (res, rid, s, e) => problem(res, 409, 'slot-taken', 'Slot already booked',
  `Resource ${rid} is not free between ${s} and ${e}.`);
const validation = (res, errors) => problem(res, 422, 'validation-error', 'Validation failed', null, { errors });
const badRequest = (res, detail) => problem(res, 400, 'bad-request', 'Bad request', detail);

function header(req, name) {
  const v = req.headers[name.toLowerCase()];
  return Array.isArray(v) ? v[0] : v;
}

// With interceptRequests: true the body is parsed into req.body; fall back to
// reading the stream so the handler also works without it.
function readBody(req) {
  if (req.body !== undefined) {
    if (typeof req.body === 'string') {
      try { return Promise.resolve(req.body ? JSON.parse(req.body) : undefined); } catch (e) { return Promise.resolve(null); }
    }
    return Promise.resolve(req.body);
  }
  return new Promise((resolve) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      if (!raw) return resolve(undefined);
      try { resolve(JSON.parse(raw)); } catch (e) { resolve(null); }
    });
    req.on('error', () => resolve(undefined));
  });
}

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
const isTime = (v) => typeof v === 'string' && RFC3339.test(v) && !isNaN(Date.parse(v));
const isEmail = (v) => typeof v === 'string' && /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v);
const toUtc = (v) => new Date(v).toISOString().replace(/\.\d{3}Z$/, 'Z');
const ACTIVE = ['pending', 'confirmed'];

function overlaps(bookings, resourceId, start, end, ignoreId) {
  const s = Date.parse(start), e = Date.parse(end);
  return Object.values(bookings).some((b) => b.id !== ignoreId && b.resource_id === resourceId &&
    ACTIVE.includes(b.status) && Date.parse(b.start_time) < e && s < Date.parse(b.end_time));
}

function paginate(items, query) {
  let limit = parseInt(query.get('limit') || '20', 10);
  if (!Number.isFinite(limit) || limit < 1) limit = 20;
  if (limit > 100) limit = 100;
  let offset = 0;
  const cursor = query.get('cursor');
  if (cursor) {
    try {
      const o = JSON.parse(Buffer.from(cursor, 'base64').toString('utf8')).o;
      if (Number.isInteger(o) && o >= 0) offset = o;
    } catch (e) { /* unknown cursor: start from the top */ }
  }
  const page = items.slice(offset, offset + limit);
  const next = offset + limit < items.length
    ? Buffer.from(JSON.stringify({ o: offset + limit })).toString('base64').replace(/=+$/, '')
    : null;
  return { data: page, next_cursor: next };
}

function newId(prefix) {
  return prefix + crypto.randomBytes(3).toString('hex');
}

// ---------------------------------------------------------------- server

const server = http.createServer(async (req, res) => {
  // Start reading the body before any await so no stream data is missed.
  const bodyPromise = readBody(req);
  try {
    const url = new URL(req.url || '/', 'http://localhost');
    const path = url.pathname.replace(/\/+$/, '') || '/';
    const method = req.method;
    const q = url.searchParams;

    // Scenarios
    const scenario = header(req, 'x-mock-scenario');
    if (scenario === 'rate-limited') {
      return problem(res, 429, 'rate-limited', 'Too many requests',
        'Rate limit of 100 requests per minute exceeded.', null, { 'Retry-After': '30' });
    }
    if (scenario === 'server-error') {
      return problem(res, 500, 'internal-server-error', 'Internal server error',
        'An unexpected error occurred. Please try again later.');
    }

    await ensureSeeded();

    // Mock-only control routes (no auth)
    if (method === 'POST' && path === `${BASE}/__mock/reset`) {
      await seed();
      return send(res, 200, { reset: true, bookings: Object.keys(seedBookings()) });
    }

    // Webhook receivers. The generated collections build these URLs as
    // {{baseUrl}}booking.confirmed, so they arrive as /v1booking.confirmed.
    // Not a spec path (the spec's webhooks are calls the API makes), so not annotated.
    const hook = path.match(/^\/v1\/?(booking\.(confirmed|cancelled))$/);
    if (hook) {
      if (method !== 'POST') return problem(res, 405, 'method-not-allowed', 'Method not allowed', `Use POST for ${hook[1]}.`, null, { Allow: 'POST' });
      const evt = await bodyPromise;
      return send(res, 200, { received: true, event: hook[1], id: evt && evt.id ? evt.id : null });
    }

    // Auth
    const apiKey = header(req, 'x-api-key');
    const authz = header(req, 'authorization');
    const bearer = typeof authz === 'string' && /^Bearer\s+\S+/i.test(authz);
    if (!apiKey && !bearer) return unauthorized(res);
    const isWrite = method !== 'GET' && method !== 'HEAD';
    const readOnly = typeof apiKey === 'string' && apiKey.startsWith('ro_');

    if (!path.startsWith(BASE + '/')) return problem(res, 404, 'not-found', 'Not found', `No route for ${method} ${path}.`);
    const route = path.slice(BASE.length);

    if (isWrite && readOnly) return forbidden(res);

    let m;

    if (method === 'POST' && (m = route.match(/^\/__mock\/bookings\/([^/]+)\/complete$/))) {
      const bookings = await pm.state.get('bookings');
      const b = bookings[m[1]];
      if (!b) return notFound(res, 'booking', m[1]);
      if (b.status !== 'confirmed') return invalidTransition(res, b, 'completed');
      b.status = 'completed';
      await pm.state.set('bookings', bookings);
      return send(res, 200, b);
    }

    // ---------------- resources
    // @endpoint GET /v1/resources
    if (method === 'GET' && route === '/resources') {
      let items = await pm.state.get('resources');
      if (q.get('type')) items = items.filter((r) => r.type === q.get('type'));
      if (q.get('location')) items = items.filter((r) => r.location === q.get('location'));
      return send(res, 200, paginate(items, q));
    }

    // @endpoint GET /v1/resources/:resource_id
    if (method === 'GET' && (m = route.match(/^\/resources\/([^/]+)$/))) {
      const r = (await pm.state.get('resources')).find((x) => x.id === m[1]);
      return r ? send(res, 200, r) : notFound(res, 'resource', m[1]);
    }

    // @endpoint GET /v1/resources/:resource_id/availability
    if (method === 'GET' && (m = route.match(/^\/resources\/([^/]+)\/availability$/))) {
      const from = q.get('from'), to = q.get('to');
      if (!isTime(from)) return badRequest(res, "Query parameter 'from' must be an RFC 3339 timestamp.");
      if (!isTime(to)) return badRequest(res, "Query parameter 'to' must be an RFC 3339 timestamp.");
      if (Date.parse(to) <= Date.parse(from)) return badRequest(res, "Query parameter 'to' must be after 'from'.");
      const r = (await pm.state.get('resources')).find((x) => x.id === m[1]);
      if (!r) return notFound(res, 'resource', m[1]);
      const winS = Date.parse(from), winE = Date.parse(to);
      const busy = Object.values(await pm.state.get('bookings'))
        .filter((b) => b.resource_id === r.id && ACTIVE.includes(b.status))
        .map((b) => [Math.max(Date.parse(b.start_time), winS), Math.min(Date.parse(b.end_time), winE)])
        .filter(([s, e]) => s < e)
        .sort((a, b) => a[0] - b[0]);
      const slots = [];
      let cursor = winS;
      for (const [s, e] of busy) {
        if (s > cursor) slots.push({ start_time: toUtc(cursor), end_time: toUtc(s) });
        cursor = Math.max(cursor, e);
      }
      if (cursor < winE) slots.push({ start_time: toUtc(cursor), end_time: toUtc(winE) });
      return send(res, 200, { resource_id: r.id, slots });
    }

    // ---------------- bookings
    // @endpoint GET /v1/bookings
    if (method === 'GET' && route === '/bookings') {
      let items = Object.values(await pm.state.get('bookings'));
      if (q.get('status')) items = items.filter((b) => b.status === q.get('status'));
      if (q.get('resource_id')) items = items.filter((b) => b.resource_id === q.get('resource_id'));
      if (q.get('customer_email')) items = items.filter((b) => b.customer.email === q.get('customer_email'));
      items.sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
      return send(res, 200, paginate(items, q));
    }

    // @endpoint POST /v1/bookings
    if (method === 'POST' && route === '/bookings') {
      const key = header(req, 'idempotency-key');
      if (!key) return badRequest(res, 'The Idempotency-Key header is required.');
      if (key.length < 8 || key.length > 64) return badRequest(res, 'Idempotency-Key must be 8 to 64 characters.');
      const idem = await pm.state.get('idempotency');
      const bookings = await pm.state.get('bookings');
      if (idem[key] && bookings[idem[key]]) {
        const original = bookings[idem[key]];
        return send(res, 201, original, { Location: `${BASE}/bookings/${original.id}`, 'Idempotent-Replayed': 'true' });
      }
      const body = await bodyPromise;
      if (body === null) return badRequest(res, 'Request body must be valid JSON.');
      const b = body || {};
      const errors = [];
      if (typeof b.resource_id !== 'string' || !b.resource_id) errors.push({ field: 'resource_id', message: 'resource_id is required' });
      if (!isTime(b.start_time)) errors.push({ field: 'start_time', message: 'start_time must be an RFC 3339 timestamp' });
      if (!isTime(b.end_time)) errors.push({ field: 'end_time', message: 'end_time must be an RFC 3339 timestamp' });
      if (isTime(b.start_time) && isTime(b.end_time) && Date.parse(b.end_time) <= Date.parse(b.start_time)) {
        errors.push({ field: 'end_time', message: 'end_time must be after start_time' });
      }
      if (!b.customer || typeof b.customer.name !== 'string' || !b.customer.name) errors.push({ field: 'customer.name', message: 'customer.name is required' });
      if (!b.customer || !isEmail(b.customer.email)) errors.push({ field: 'customer.email', message: 'customer.email must be an email address' });
      if (b.notes !== undefined && (typeof b.notes !== 'string' || b.notes.length > 1000)) errors.push({ field: 'notes', message: 'notes must be a string of at most 1000 characters' });
      if (errors.length) return validation(res, errors);
      const resources = await pm.state.get('resources');
      if (!resources.find((r) => r.id === b.resource_id)) return notFound(res, 'resource', b.resource_id);
      if (overlaps(bookings, b.resource_id, b.start_time, b.end_time)) return slotTaken(res, b.resource_id, b.start_time, b.end_time);
      const booking = {
        id: newId('bkg_'), resource_id: b.resource_id, status: 'pending',
        start_time: b.start_time, end_time: b.end_time,
        customer: { name: b.customer.name, email: b.customer.email },
        notes: b.notes === undefined ? null : b.notes, cancel_reason: null,
        created_at: toUtc(Date.now()),
      };
      bookings[booking.id] = booking;
      idem[key] = booking.id;
      await pm.state.set('bookings', bookings);
      await pm.state.set('idempotency', idem);
      return send(res, 201, booking, { Location: `${BASE}/bookings/${booking.id}` });
    }

    // @endpoint GET /v1/bookings/:booking_id
    // @endpoint PATCH /v1/bookings/:booking_id
    if ((m = route.match(/^\/bookings\/([^/]+)$/)) && (method === 'GET' || method === 'PATCH')) {
      const bookings = await pm.state.get('bookings');
      const b = bookings[m[1]];
      if (!b) return notFound(res, 'booking', m[1]);
      if (method === 'GET') return send(res, 200, b);

      const body = await bodyPromise;
      if (body === null) return badRequest(res, 'Request body must be valid JSON.');
      const u = body || {};
      const errors = [];
      const known = ['start_time', 'end_time', 'notes'].filter((k) => u[k] !== undefined);
      if (!known.length) errors.push({ field: 'body', message: 'Provide at least one of start_time, end_time, notes' });
      if (u.start_time !== undefined && !isTime(u.start_time)) errors.push({ field: 'start_time', message: 'start_time must be an RFC 3339 timestamp' });
      if (u.end_time !== undefined && !isTime(u.end_time)) errors.push({ field: 'end_time', message: 'end_time must be an RFC 3339 timestamp' });
      if (u.notes !== undefined && (typeof u.notes !== 'string' || u.notes.length > 1000)) errors.push({ field: 'notes', message: 'notes must be a string of at most 1000 characters' });
      const start = u.start_time || b.start_time, end = u.end_time || b.end_time;
      if (!errors.length && Date.parse(end) <= Date.parse(start)) errors.push({ field: 'end_time', message: 'end_time must be after start_time' });
      if (errors.length) return validation(res, errors);
      if (!ACTIVE.includes(b.status)) return invalidTransition(res, b, 'rescheduled');
      if (overlaps(bookings, b.resource_id, start, end, b.id)) return slotTaken(res, b.resource_id, start, end);
      b.start_time = start;
      b.end_time = end;
      if (u.notes !== undefined) b.notes = u.notes;
      await pm.state.set('bookings', bookings);
      return send(res, 200, b);
    }

    // @endpoint POST /v1/bookings/:booking_id/confirm
    // @endpoint POST /v1/bookings/:booking_id/cancel
    if (method === 'POST' && (m = route.match(/^\/bookings\/([^/]+)\/(confirm|cancel)$/))) {
      const bookings = await pm.state.get('bookings');
      const b = bookings[m[1]];
      if (!b) return notFound(res, 'booking', m[1]);
      if (m[2] === 'confirm') {
        if (b.status !== 'pending') return invalidTransition(res, b, 'confirmed');
        // BREAK_CONFIRM: answer 200 without changing the status (silent bug).
        if (BREAK_CONFIRM) return send(res, 200, b);
        b.status = 'confirmed';
      } else {
        const body = await bodyPromise;
        if (!ACTIVE.includes(b.status)) return invalidTransition(res, b, 'cancelled');
        b.status = 'cancelled';
        b.cancel_reason = body && typeof body.reason === 'string' ? body.reason.slice(0, 500) : null;
      }
      await pm.state.set('bookings', bookings);
      return send(res, 200, b);
    }

    // ---------------- webhook subscriptions
    // @endpoint POST /v1/webhook-subscriptions
    if (method === 'POST' && route === '/webhook-subscriptions') {
      const body = await bodyPromise;
      if (body === null) return badRequest(res, 'Request body must be valid JSON.');
      const s = body || {};
      const errors = [];
      if (typeof s.url !== 'string' || !/^https:\/\/\S+$/.test(s.url)) errors.push({ field: 'url', message: 'url must be an HTTPS URL' });
      const allowed = ['booking.confirmed', 'booking.cancelled'];
      if (!Array.isArray(s.events) || !s.events.length || s.events.some((e) => !allowed.includes(e))) {
        errors.push({ field: 'events', message: 'events must be a non-empty list of booking.confirmed, booking.cancelled' });
      }
      if (errors.length) return validation(res, errors);
      const subs = await pm.state.get('subscriptions');
      const sub = { id: newId('whs_'), url: s.url, events: s.events, secret: 'whsec_' + crypto.randomBytes(12).toString('hex'), created_at: toUtc(Date.now()) };
      subs[sub.id] = { id: sub.id, url: sub.url, events: sub.events, created_at: sub.created_at };
      await pm.state.set('subscriptions', subs);
      return send(res, 201, sub);
    }

    return problem(res, 404, 'not-found', 'Not found', `No route for ${method} ${path}.`);
  } catch (err) {
    if (!res.headersSent) {
      return problem(res, 500, 'internal-server-error', 'Internal server error',
        'An unexpected error occurred. Please try again later.');
    }
    res.end();
  }
});

server.listen(PORT);
