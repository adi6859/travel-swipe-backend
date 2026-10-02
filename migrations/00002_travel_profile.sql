-- +goose Up
-- Travel profile: how a user likes to travel. These answers feed matching later,
-- so vocabularies are closed: small enums are CHECK constraints, and interests
-- are a curated table that grows through migrations (retire with active = false,
-- never delete, so existing selections stay valid).

CREATE TABLE interests (
    slug       TEXT        PRIMARY KEY,
    label      TEXT        NOT NULL,
    category   TEXT        NOT NULL,
    sort_order INT         NOT NULL DEFAULT 0,
    active     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT interests_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{1,39}$'),
    CONSTRAINT interests_label_length CHECK (char_length(label) BETWEEN 1 AND 60),
    CONSTRAINT interests_category_valid CHECK (category IN ('outdoors', 'culture', 'food_drink', 'leisure'))
);

INSERT INTO interests (slug, label, category, sort_order) VALUES
    ('trekking',      'Trekking',          'outdoors',   10),
    ('camping',       'Camping',           'outdoors',   20),
    ('mountains',     'Mountains',         'outdoors',   30),
    ('beaches',       'Beaches',           'outdoors',   40),
    ('wildlife',      'Wildlife & safaris','outdoors',   50),
    ('water_sports',  'Water sports',      'outdoors',   60),
    ('cycling',       'Cycling',           'outdoors',   70),
    ('heritage',      'Heritage sites',    'culture',   110),
    ('architecture',  'Architecture',      'culture',   120),
    ('museums',       'Museums',           'culture',   130),
    ('spirituality',  'Spiritual places',  'culture',   140),
    ('festivals',     'Festivals',         'culture',   150),
    ('local_culture', 'Local culture',     'culture',   160),
    ('street_food',   'Street food',       'food_drink',210),
    ('fine_dining',   'Fine dining',       'food_drink',220),
    ('cafes',         'Cafe hopping',      'food_drink',230),
    ('nightlife',     'Nightlife',         'food_drink',240),
    ('photography',   'Photography',       'leisure',   310),
    ('wellness',      'Wellness & yoga',   'leisure',   320),
    ('road_trips',    'Road trips',        'leisure',   330),
    ('shopping',      'Shopping',          'leisure',   340),
    ('music',         'Live music',        'leisure',   350),
    ('art',           'Art & galleries',   'leisure',   360);

CREATE TABLE travel_profiles (
    user_id       UUID        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    travel_styles TEXT[]      NOT NULL DEFAULT '{}',
    languages     TEXT[]      NOT NULL DEFAULT '{}',
    budget_band   TEXT        NULL,
    group_size    TEXT        NULL,
    pace          TEXT        NULL,
    smoking       TEXT        NULL,
    drinking      TEXT        NULL,
    diet          TEXT        NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT travel_profiles_styles_valid CHECK (
        travel_styles <@ ARRAY['backpacker', 'comfort', 'luxury', 'adventure', 'slow', 'offbeat', 'workation']::TEXT[]
        AND cardinality(travel_styles) <= 3
        AND array_position(travel_styles, NULL) IS NULL
    ),
    CONSTRAINT travel_profiles_languages_valid CHECK (
        cardinality(languages) <= 8
        AND array_position(languages, NULL) IS NULL
        AND array_to_string(languages, ',') ~ '^([a-z]{2}(,[a-z]{2})*)?$'
    ),
    CONSTRAINT travel_profiles_budget_valid CHECK (budget_band IN ('shoestring', 'budget', 'mid_range', 'premium', 'luxury')),
    CONSTRAINT travel_profiles_group_size_valid CHECK (group_size IN ('small', 'medium', 'large', 'any')),
    CONSTRAINT travel_profiles_pace_valid CHECK (pace IN ('relaxed', 'balanced', 'packed')),
    CONSTRAINT travel_profiles_smoking_valid CHECK (smoking IN ('never', 'sometimes', 'regularly')),
    CONSTRAINT travel_profiles_drinking_valid CHECK (drinking IN ('never', 'sometimes', 'regularly')),
    CONSTRAINT travel_profiles_diet_valid CHECK (diet IN ('no_preference', 'vegetarian', 'eggetarian', 'vegan', 'jain', 'non_vegetarian'))
);

CREATE TABLE travel_profile_interests (
    user_id       UUID NOT NULL REFERENCES travel_profiles (user_id) ON DELETE CASCADE,
    interest_slug TEXT NOT NULL REFERENCES interests (slug),
    PRIMARY KEY (user_id, interest_slug)
);

-- Matching looks up users by interest.
CREATE INDEX travel_profile_interests_by_interest ON travel_profile_interests (interest_slug, user_id);

-- +goose Down
DROP TABLE IF EXISTS travel_profile_interests;
DROP TABLE IF EXISTS travel_profiles;
DROP TABLE IF EXISTS interests;
