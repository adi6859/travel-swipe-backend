-- +goose Up
-- Trip catalogue plus the ingestion records it is built from.
--
-- Ingestion side (adapted from scenes-scraper): a source carries the rights we
-- hold for its content; each import run records counts; source_listings keep
-- the latest payload per external listing with stale detection.
-- Catalogue side: providers, trips, dated departures (with price history for
-- alerts), media, itinerary, inclusions. Booking happens on the provider site.

-- Extensions are database-wide; keep them in public so every schema sees them.
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

INSERT INTO interests (slug, label, category, sort_order) VALUES
    ('nature', 'Nature', 'outdoors', 75);

CREATE TABLE ingest_sources (
    id                  UUID        PRIMARY KEY,
    source_key          TEXT        NOT NULL UNIQUE,
    display_name        TEXT        NOT NULL,
    base_url            TEXT        NOT NULL,
    enabled             BOOLEAN     NOT NULL DEFAULT TRUE,
    auto_publish        BOOLEAN     NOT NULL DEFAULT FALSE,
    rights_basis        TEXT        NOT NULL,
    image_allowed       BOOLEAN     NOT NULL DEFAULT FALSE,
    description_allowed BOOLEAN     NOT NULL DEFAULT FALSE,
    attribution_text    TEXT        NULL,
    evidence_ref        TEXT        NOT NULL,
    rights_expires_at   TIMESTAMPTZ NULL,
    missing_grace_runs  INT         NOT NULL DEFAULT 2,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ingest_sources_key_format CHECK (source_key ~ '^[a-z][a-z0-9_]{1,39}$'),
    CONSTRAINT ingest_sources_basis_valid CHECK (rights_basis IN
        ('first_party', 'contract', 'affiliate', 'api_terms', 'official_publication', 'research_only')),
    CONSTRAINT ingest_sources_research_never_published CHECK (rights_basis <> 'research_only' OR NOT auto_publish),
    CONSTRAINT ingest_sources_grace_positive CHECK (missing_grace_runs BETWEEN 1 AND 30)
);

CREATE TABLE ingest_runs (
    id              UUID        PRIMARY KEY,
    source_id       UUID        NOT NULL REFERENCES ingest_sources (id) ON DELETE RESTRICT,
    trigger         TEXT        NOT NULL,
    complete        BOOLEAN     NOT NULL,
    status          TEXT        NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ NULL,
    received_count  INT         NOT NULL DEFAULT 0,
    inserted_count  INT         NOT NULL DEFAULT 0,
    updated_count   INT         NOT NULL DEFAULT 0,
    unchanged_count INT         NOT NULL DEFAULT 0,
    rejected_count  INT         NOT NULL DEFAULT 0,
    expired_count   INT         NOT NULL DEFAULT 0,
    rejections      JSONB       NOT NULL DEFAULT '[]',
    error_summary   TEXT        NULL,
    CONSTRAINT ingest_runs_trigger_valid CHECK (trigger IN ('scraper', 'manual', 'seed')),
    CONSTRAINT ingest_runs_status_valid CHECK (status IN ('running', 'succeeded', 'partial', 'failed'))
);

CREATE INDEX ingest_runs_by_source ON ingest_runs (source_id, started_at DESC);

CREATE TABLE providers (
    id           UUID         PRIMARY KEY,
    slug         TEXT         NOT NULL UNIQUE,
    name         TEXT         NOT NULL,
    website_url  TEXT         NULL,
    rating       NUMERIC(2,1) NULL,
    review_count INT          NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT providers_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,59}$'),
    CONSTRAINT providers_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT providers_rating_range CHECK (rating BETWEEN 0 AND 5),
    CONSTRAINT providers_reviews_positive CHECK (review_count >= 0)
);

CREATE TABLE trips (
    id                UUID         PRIMARY KEY,
    provider_id       UUID         NOT NULL REFERENCES providers (id) ON DELETE RESTRICT,
    source_id         UUID         NOT NULL REFERENCES ingest_sources (id) ON DELETE RESTRICT,
    slug              TEXT         NOT NULL UNIQUE,
    title             TEXT         NOT NULL,
    summary           TEXT         NULL,
    description       TEXT         NULL,
    destination       TEXT         NOT NULL,
    region            TEXT         NULL,
    country_code      CHAR(2)      NOT NULL DEFAULT 'IN',
    latitude          NUMERIC(9,6) NULL,
    longitude         NUMERIC(9,6) NULL,
    duration_days     INT          NOT NULL,
    duration_nights   INT          NOT NULL,
    difficulty        TEXT         NULL,
    max_group_size    INT          NULL,
    min_age           INT          NULL,
    rating            NUMERIC(2,1) NULL,
    review_count      INT          NULL,
    booking_url       TEXT         NOT NULL,
    attribution_text  TEXT         NULL,
    status            TEXT         NOT NULL,
    status_reason     TEXT         NULL,
    published_at      TIMESTAMPTZ  NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    search_vector     TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(destination, '') || ' ' || coalesce(region, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(summary, '')), 'B')
    ) STORED,
    CONSTRAINT trips_title_length CHECK (char_length(title) BETWEEN 1 AND 150),
    CONSTRAINT trips_summary_length CHECK (char_length(summary) <= 300),
    CONSTRAINT trips_description_length CHECK (char_length(description) <= 10000),
    CONSTRAINT trips_destination_length CHECK (char_length(destination) BETWEEN 1 AND 100),
    CONSTRAINT trips_country_format CHECK (country_code ~ '^[A-Z]{2}$'),
    CONSTRAINT trips_coordinates_pair CHECK ((latitude IS NULL) = (longitude IS NULL)),
    CONSTRAINT trips_latitude_range CHECK (latitude BETWEEN -90 AND 90),
    CONSTRAINT trips_longitude_range CHECK (longitude BETWEEN -180 AND 180),
    CONSTRAINT trips_duration_range CHECK (duration_days BETWEEN 1 AND 60 AND duration_nights BETWEEN 0 AND duration_days),
    CONSTRAINT trips_difficulty_valid CHECK (difficulty IN ('easy', 'moderate', 'difficult', 'challenging')),
    CONSTRAINT trips_group_size_range CHECK (max_group_size BETWEEN 1 AND 200),
    CONSTRAINT trips_min_age_range CHECK (min_age BETWEEN 0 AND 99),
    CONSTRAINT trips_rating_range CHECK (rating BETWEEN 0 AND 5),
    CONSTRAINT trips_reviews_positive CHECK (review_count >= 0),
    CONSTRAINT trips_booking_https CHECK (booking_url ~ '^https://'),
    CONSTRAINT trips_status_valid CHECK (status IN ('draft', 'published', 'hidden', 'expired')),
    CONSTRAINT trips_published_at_set CHECK (status <> 'published' OR published_at IS NOT NULL)
);

CREATE INDEX trips_published ON trips (id) WHERE status = 'published';
CREATE INDEX trips_search ON trips USING GIN (search_vector);
CREATE INDEX trips_title_trgm ON trips USING GIN (title gin_trgm_ops);
CREATE INDEX trips_destination_trgm ON trips USING GIN (destination gin_trgm_ops);
CREATE INDEX trips_by_provider ON trips (provider_id);

CREATE TABLE source_listings (
    id                       UUID        PRIMARY KEY,
    source_id                UUID        NOT NULL REFERENCES ingest_sources (id) ON DELETE RESTRICT,
    external_id              TEXT        NOT NULL,
    source_url               TEXT        NOT NULL,
    content_hash             TEXT        NOT NULL,
    payload                  JSONB       NOT NULL,
    trip_id                  UUID        NULL REFERENCES trips (id) ON DELETE SET NULL,
    first_seen_run_id        UUID        NOT NULL REFERENCES ingest_runs (id) ON DELETE RESTRICT,
    last_seen_run_id         UUID        NOT NULL REFERENCES ingest_runs (id) ON DELETE RESTRICT,
    first_seen_at            TIMESTAMPTZ NOT NULL,
    last_seen_at             TIMESTAMPTZ NOT NULL,
    consecutive_missing_runs INT         NOT NULL DEFAULT 0,
    stale_at                 TIMESTAMPTZ NULL,
    CONSTRAINT source_listings_identity UNIQUE (source_id, external_id),
    CONSTRAINT source_listings_external_id_length CHECK (char_length(external_id) BETWEEN 1 AND 200),
    CONSTRAINT source_listings_missing_positive CHECK (consecutive_missing_runs >= 0)
);

CREATE INDEX source_listings_by_trip ON source_listings (trip_id);

CREATE TABLE trip_interests (
    trip_id       UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    interest_slug TEXT NOT NULL REFERENCES interests (slug),
    PRIMARY KEY (trip_id, interest_slug)
);

CREATE INDEX trip_interests_by_interest ON trip_interests (interest_slug, trip_id);

CREATE TABLE trip_departures (
    id                   UUID        PRIMARY KEY,
    trip_id              UUID        NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    departure_key        TEXT        NOT NULL,
    start_date           DATE        NOT NULL,
    end_date             DATE        NOT NULL,
    departure_city       TEXT        NULL,
    price_paise          BIGINT      NULL,
    original_price_paise BIGINT      NULL,
    currency             CHAR(3)     NOT NULL DEFAULT 'INR',
    availability         TEXT        NOT NULL DEFAULT 'unknown',
    seats_left           INT         NULL,
    last_seen_at         TIMESTAMPTZ NOT NULL,
    removed_at           TIMESTAMPTZ NULL,
    CONSTRAINT trip_departures_identity UNIQUE (trip_id, departure_key),
    CONSTRAINT trip_departures_dates CHECK (end_date >= start_date),
    CONSTRAINT trip_departures_price_positive CHECK (price_paise >= 0),
    CONSTRAINT trip_departures_original_price CHECK (original_price_paise >= price_paise),
    CONSTRAINT trip_departures_currency CHECK (currency = 'INR'),
    CONSTRAINT trip_departures_availability CHECK (availability IN ('available', 'filling_fast', 'waitlist', 'sold_out', 'unknown')),
    CONSTRAINT trip_departures_seats_positive CHECK (seats_left >= 0)
);

CREATE INDEX trip_departures_upcoming ON trip_departures (trip_id, start_date) WHERE removed_at IS NULL;
CREATE INDEX trip_departures_by_city ON trip_departures (lower(departure_city), start_date) WHERE removed_at IS NULL;

CREATE TABLE trip_price_history (
    departure_id UUID        NOT NULL REFERENCES trip_departures (id) ON DELETE CASCADE,
    observed_at  TIMESTAMPTZ NOT NULL,
    price_paise  BIGINT      NULL,
    PRIMARY KEY (departure_id, observed_at)
);

CREATE TABLE trip_media (
    id              UUID        PRIMARY KEY,
    trip_id         UUID        NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    url             TEXT        NOT NULL,
    width           INT         NULL,
    height          INT         NULL,
    position        INT         NOT NULL,
    display_allowed BOOLEAN     NOT NULL DEFAULT FALSE,
    CONSTRAINT trip_media_identity UNIQUE (trip_id, url),
    CONSTRAINT trip_media_https CHECK (url ~ '^https://'),
    CONSTRAINT trip_media_dimensions CHECK (width > 0 AND height > 0)
);

CREATE TABLE trip_itinerary_days (
    trip_id     UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    day_number  INT  NOT NULL,
    title       TEXT NOT NULL,
    description TEXT NULL,
    PRIMARY KEY (trip_id, day_number),
    CONSTRAINT trip_itinerary_day_range CHECK (day_number BETWEEN 1 AND 60),
    CONSTRAINT trip_itinerary_title_length CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT trip_itinerary_description_length CHECK (char_length(description) <= 2000)
);

CREATE TABLE trip_inclusions (
    trip_id  UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    kind     TEXT NOT NULL,
    position INT  NOT NULL,
    text     TEXT NOT NULL,
    PRIMARY KEY (trip_id, kind, position),
    CONSTRAINT trip_inclusions_kind CHECK (kind IN ('included', 'excluded')),
    CONSTRAINT trip_inclusions_text_length CHECK (char_length(text) BETWEEN 1 AND 300)
);

CREATE TABLE trip_outbound_clicks (
    id           UUID        PRIMARY KEY,
    trip_id      UUID        NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    departure_id UUID        NULL REFERENCES trip_departures (id) ON DELETE SET NULL,
    user_id      UUID        NULL REFERENCES users (id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX trip_outbound_clicks_by_trip ON trip_outbound_clicks (trip_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS trip_outbound_clicks;
DROP TABLE IF EXISTS trip_inclusions;
DROP TABLE IF EXISTS trip_itinerary_days;
DROP TABLE IF EXISTS trip_media;
DROP TABLE IF EXISTS trip_price_history;
DROP TABLE IF EXISTS trip_departures;
DROP TABLE IF EXISTS trip_interests;
DROP TABLE IF EXISTS source_listings;
DROP TABLE IF EXISTS trips;
DROP TABLE IF EXISTS providers;
DROP TABLE IF EXISTS ingest_runs;
DROP TABLE IF EXISTS ingest_sources;
DELETE FROM travel_profile_interests WHERE interest_slug = 'nature';
DELETE FROM interests WHERE slug = 'nature';
