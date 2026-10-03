# travel-swipe-backend

Backend for the travel-tech platform: discover a trip, find your people, form your
crew, travel together.

Go + Gin + PostgreSQL modular monolith. Fully independent from Krozd (own module,
config, database, secrets, and deployment); selected Krozd patterns were copied and
adapted, never imported.

## Layout

```text
api                OpenAPI 3.1 contract (api/openapi.yaml), checked against the router in tests
cmd/api            HTTP API entrypoint (manual wiring, graceful shutdown)
cmd/migrate        Goose migrations runner (API never auto-migrates)
cmd/seed           loads sample treks through the ingest pipeline (dev only)
internal/config    env-backed config with production fail-fast validation
internal/platform  database, logger, httpx (error envelope, binding), middleware
internal/modules   feature modules (auth, users, travelprofile, ingest, catalog) - one package each
internal/server    router assembly
migrations         embedded Goose SQL
pkg                errors, clock, ctxutil, require
deploy             Dockerfile, docker-compose
```

## Local development

```powershell
copy .env.example .env      # then fill AUTH_JWT_SECRET and AUTH_OTP_SECRET
docker compose -f deploy/docker-compose.yml up -d postgres
$env:DATABASE_URL="postgres://travel:travel@localhost:5433/travel_swipe?sslmode=disable"
go run ./cmd/migrate up
go run ./cmd/seed           # optional: 15 sample treks (fictional providers, example.com links)
go run ./cmd/api
```

The seed is idempotent: a second run reports every listing as unchanged. It
refuses to run when `APP_ENV=production`.

## Tests

```powershell
go test ./...                                   # unit tests
$env:TEST_DATABASE_URL="postgres://travel:travel@localhost:5433/travel_swipe_test?sslmode=disable"
go test -count=1 ./...                          # + database integration tests
```

Integration tests create a throwaway schema per test in `TEST_DATABASE_URL` and
drop it afterwards; they skip when the variable is unset. Config tests use an
explicit environment map, so variables set in your shell do not affect them.

`api/openapi.yaml` is the API contract. `go test ./api` fails if a route is added
or removed without updating it. Lint it with `make spec-lint` (needs Node).

## Docker

```powershell
docker build -f deploy/Dockerfile -t travel-swipe-api .   # distroless, non-root, api + migrate
docker compose -f deploy/docker-compose.yml --profile app up --build   # postgres, migrate, api (needs .env)
```

The image entrypoint is `/app/api`; run migrations with
`--entrypoint /app/migrate <image> up`. The API never migrates on startup.

## Auth API (phone OTP)

| Endpoint | Auth | Notes |
|---|---|---|
| `POST /api/v1/auth/otp/request` `{phone}` | none | 202 `{challenge_id, expires_at, resend_after}`. Sign-up, login and resend in one call. 30s cooldown, 10/phone/day |
| `POST /api/v1/auth/otp/verify` `{phone, code, device_name?}` | none | 200 tokens + `user` + `is_new_user`. Creates the account on first login |
| `POST /api/v1/auth/refresh` `{refresh_token}` | none | Rotates the refresh token. Reusing an old one revokes the session |
| `POST /api/v1/auth/logout` | Bearer | 204, revokes the current session (and its access token) |
| `POST /api/v1/auth/logout-all` | Bearer | 200 `{revoked_sessions}` |

Clients must serialize refresh calls: two parallel refreshes with the same token
count as reuse and sign the device out.

With `SMS_PROVIDER=console` (non-production only) the OTP is printed in the server
log as `DEV SMS (not delivered)`.

## Users API

| Endpoint | Auth | Notes |
|---|---|---|
| `GET /api/v1/users/me` | Bearer | Account plus profile, `profile_complete` = display name and date of birth set |
| `PATCH /api/v1/users/me` | Bearer | Partial update of profile fields |

PATCH semantics: an omitted key is unchanged, `null` clears the field, and a value sets it.
Unknown keys (including `phone`, `status`, `id`) are rejected with 400.
Editable fields: `display_name` (≤60 chars), `bio` (≤500), `avatar`
(`{url (https), width, height, mime_type (jpeg/png/webp/heic), size_bytes (≤10MB)}`),
`date_of_birth` (`YYYY-MM-DD`, age 18–120), `gender`
(`male|female|non_binary|prefer_not_to_say`), `home_city` (≤100), `country_code`
(ISO 3166-1 alpha-2). All invalid fields are reported together in `error.details`.

## Travel profile API

| Endpoint | Auth | Notes |
|---|---|---|
| `GET /api/v1/travel-profile/options` | Bearer | Every allowed value with display labels, for pickers |
| `GET /api/v1/users/me/travel-profile` | Bearer | Empty lists and nulls until first save |
| `PUT /api/v1/users/me/travel-profile` | Bearer | Full replacement; omitted or null fields are cleared |

Fields: `travel_styles` (≤3), `interests` (≤10), `languages` (≤8, ISO 639-1),
`budget_band`, `group_size`, `pace`, `smoking`, `drinking`, `diet`. `complete` is
true with at least one style, three interests, one language and a budget band.

Vocabularies are closed because they feed matching. Small enums live in
`internal/modules/travelprofile/vocab.go` and as CHECK constraints in the
migration; a database test fails if the two disagree. Interests live in the
`interests` table: add new ones with a migration, retire old ones with
`active = false` (never delete; existing selections reference them).

## Trips API (Explore)

| Endpoint | Auth | Notes |
|---|---|---|
| `GET /api/v1/trips` | Bearer | Published trips. Filters `q`, `category`, `interest`, `difficulty`, `min_days`, `max_days`, `max_price_inr`, `month` (`YYYY-MM`), `departure_city`; `sort` = `relevance` (default with `q`), `popular` (default), `price_asc`, `soonest`; `limit` ≤50; `cursor` |
| `GET /api/v1/trips/categories` | Bearer | Treks, Mountains, Beaches, Road Trips, Weekend (≤3 days), International, with counts |
| `GET /api/v1/trips/{id}` | Bearer | Detail with itinerary, inclusions, upcoming departures. Expired trips stay viewable (`bookable: false`) |
| `POST /api/v1/trips/{id}/outbound` `{departure_id?}` | Bearer | Records the click and returns the provider booking `url` |

Prices are integer paise with `currency: "INR"`. "Upcoming" means on or after
today's date in India; sold-out departures are ignored for `next_departure_date`,
`min_price_paise` and the departure filters. `month`, `departure_city` and
`max_price_inr` must match the same departure. Cursors are bound to the
filters that produced them; reusing one with different filters is a 400.

## Catalogue ingest (admin)

Trips come from **sources** (a partner feed, a scraper, the seed). Each source
records why we may use its content (`rights_basis`, `evidence_ref`) and what we
may show: `image_allowed`, `description_allowed`, `attribution_text`,
`rights_expires_at`. Facts (title, destination, dates, prices, booking link) are
always kept; text and images are stored or shown only when allowed.
`research_only` sources never publish, and trips disappear from the app as soon
as their source's rights lapse.

Admin routes live under `/admin/v1` behind a static bearer token whose SHA-256
hash is in `ADMIN_API_TOKEN_SHA256` (see `.env.example`); with no hash
configured the routes are not registered at all.

| Endpoint | Notes |
|---|---|
| `GET /admin/v1/sources` | All sources |
| `PUT /admin/v1/sources/{key}` | Create or update a source. Rights changes apply to existing trips immediately |
| `POST /admin/v1/sources/{key}/imports` `{trigger?, complete?, listings}` | Import up to 200 listings (contract in `api/openapi.yaml`, example in `cmd/seed/treks.json`) |
| `GET /admin/v1/sources/{key}/runs` | Recent runs with counts and per-listing rejections |
| `GET /admin/v1/trips?status=&source=` | Trips in any status |
| `POST /admin/v1/trips/{id}/publish` | Fails with 409 if source rights do not allow publishing |
| `POST /admin/v1/trips/{id}/hide` `{reason}` | Hidden trips stay hidden across imports |

Import behaviour: invalid listings are rejected one by one and the rest are
applied in one transaction; imports for a source run one at a time; unchanged
listings are skipped by content hash; departure prices are kept as history.
Send `complete: true` when the batch is the source's whole catalogue: a listing
missing from `missing_grace_runs` consecutive complete runs expires, and is
published again if it reappears.

```powershell
$h = @{ Authorization = "Bearer $env:ADMIN_TOKEN" }
Invoke-RestMethod -Method Post -Uri http://localhost:8080/admin/v1/sources/acme_feed/imports `
  -Headers $h -ContentType application/json -InFile batch.json
```

## Error contract

Every error response:

```json
{"error": {"code": "invalid_argument", "message": "request validation failed",
           "details": {"phone": "is required"}, "request_id": "…", "retryable": false}}
```
