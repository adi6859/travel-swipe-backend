# travel-swipe-backend

Backend for the travel-tech platform: discover a trip, find your people, form your
crew, travel together.

Go + Gin + PostgreSQL modular monolith. Fully independent from Krozd (own module,
config, database, secrets, and deployment); selected Krozd patterns were copied and
adapted, never imported.

## Layout

```text
cmd/api            HTTP API entrypoint (manual wiring, graceful shutdown)
cmd/migrate        Goose migrations runner (API never auto-migrates)
internal/config    env-backed config with production fail-fast validation
internal/platform  database, logger, httpx (error envelope, binding), middleware
internal/modules   feature modules (auth, users, ...) - one package each
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
go run ./cmd/api
```

## Tests

```powershell
go test ./...                                   # unit tests
$env:TEST_DATABASE_URL="postgres://travel:travel@localhost:5433/travel_swipe_test?sslmode=disable"
go test -count=1 ./...                          # + database integration tests (resets that DB)
```

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

## Error contract

Every error response:

```json
{"error": {"code": "invalid_argument", "message": "request validation failed",
           "details": {"phone": "is required"}, "request_id": "…", "retryable": false}}
```
