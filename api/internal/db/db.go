package db

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_PORT", "5432"),
		getEnv("DB_USER", "calendar"),
		getEnv("DB_PASSWORD", "calendar"),
		getEnv("DB_NAME", "calendar"),
	)
	return pgxpool.New(ctx, dsn)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

		CREATE TABLE IF NOT EXISTS users (
			id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			username      TEXT        NOT NULL UNIQUE,
			email         TEXT        NOT NULL UNIQUE,
			password_hash TEXT        NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS calendars (
			id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name        TEXT        NOT NULL,
			description TEXT        NOT NULL DEFAULT '',
			is_default  BOOLEAN     NOT NULL DEFAULT false,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE UNIQUE INDEX IF NOT EXISTS calendars_one_default_per_user
			ON calendars (owner_id) WHERE is_default = true;

		CREATE TABLE IF NOT EXISTS events (
			id               UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			owner_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			calendar_id      UUID        NOT NULL REFERENCES calendars(id) ON DELETE RESTRICT,
			title            TEXT        NOT NULL,
			description      TEXT        NOT NULL DEFAULT '',
			location         TEXT        NOT NULL DEFAULT '',
			start_time       TIMESTAMPTZ NOT NULL,
			end_time         TIMESTAMPTZ NOT NULL,
			attendees        TEXT[]      NOT NULL DEFAULT '{}',
			reminder_minutes INT         NOT NULL DEFAULT 0,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS recurring_events (
			id               UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			owner_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			calendar_id      UUID        NOT NULL REFERENCES calendars(id) ON DELETE RESTRICT,
			title            TEXT        NOT NULL,
			description      TEXT        NOT NULL DEFAULT '',
			location         TEXT        NOT NULL DEFAULT '',
			duration         BIGINT      NOT NULL,
			attendees        TEXT[]      NOT NULL DEFAULT '{}',
			reminder_minutes INT         NOT NULL DEFAULT 0,
			frequency        TEXT        NOT NULL,
			interval         INT         NOT NULL DEFAULT 1,
			days_of_week     INT[]       NOT NULL DEFAULT '{}',
			end_date         TIMESTAMPTZ,
			max_occurrences  INT,
			start_time       TIMESTAMPTZ NOT NULL,
			generated_until  TIMESTAMPTZ NOT NULL,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS recurring_event_id UUID REFERENCES recurring_events(id) ON DELETE CASCADE;

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS all_day BOOLEAN NOT NULL DEFAULT false;

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'UTC';

		ALTER TABLE recurring_events
			ADD COLUMN IF NOT EXISTS all_day BOOLEAN NOT NULL DEFAULT false;

		ALTER TABLE recurring_events
			ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'UTC';

		CREATE UNIQUE INDEX IF NOT EXISTS events_recurring_start_uniq
			ON events (recurring_event_id, start_time)
			WHERE recurring_event_id IS NOT NULL;

		CREATE TABLE IF NOT EXISTS categories (
			id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			owner_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name       TEXT        NOT NULL,
			color      TEXT        NOT NULL DEFAULT '#4285F4',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (owner_id, name)
		);

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS category_id UUID REFERENCES categories(id) ON DELETE SET NULL;

		ALTER TABLE recurring_events
			ADD COLUMN IF NOT EXISTS category_id UUID REFERENCES categories(id) ON DELETE SET NULL;

		CREATE TABLE IF NOT EXISTS event_invitations (
			id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			event_id   UUID        NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			email      TEXT        NOT NULL,
			status     TEXT        NOT NULL DEFAULT 'pending_send',
			token      UUID        NOT NULL DEFAULT uuid_generate_v4() UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (event_id, email)
		);

		CREATE TABLE IF NOT EXISTS calendar_shares (
			id                   UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			calendar_id          UUID        NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
			owner_id             UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			shared_with_user_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			permission           TEXT        NOT NULL DEFAULT 'view' CHECK (permission IN ('view', 'edit')),
			created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (calendar_id, shared_with_user_id)
		);

		CREATE INDEX IF NOT EXISTS events_calendar_id_idx ON events (calendar_id);

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public';

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS reminders JSONB NOT NULL DEFAULT '[]';

		ALTER TABLE recurring_events
			ADD COLUMN IF NOT EXISTS reminders JSONB NOT NULL DEFAULT '[]';

		ALTER TABLE events
			ADD COLUMN IF NOT EXISTS search_vector tsvector;

		CREATE INDEX IF NOT EXISTS events_search_idx ON events USING GIN (search_vector);

		CREATE OR REPLACE FUNCTION events_search_update() RETURNS trigger AS $$
		BEGIN
			NEW.search_vector :=
				setweight(to_tsvector('english', coalesce(NEW.title, '')), 'A') ||
				setweight(to_tsvector('english', coalesce(NEW.description, '')), 'B') ||
				setweight(to_tsvector('english', coalesce(NEW.location, '')), 'C') ||
				setweight(to_tsvector('english',
					regexp_replace(coalesce(array_to_string(NEW.attendees, ' '), ''), '[@.]', ' ', 'g')
				), 'D');
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;

		UPDATE events SET search_vector =
			setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
			setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
			setweight(to_tsvector('english', coalesce(location, '')), 'C') ||
			setweight(to_tsvector('english',
				regexp_replace(coalesce(array_to_string(attendees, ' '), ''), '[@.]', ' ', 'g')
			), 'D');

		DROP TRIGGER IF EXISTS events_search_trigger ON events;
		CREATE TRIGGER events_search_trigger
			BEFORE INSERT OR UPDATE ON events
			FOR EACH ROW EXECUTE FUNCTION events_search_update();
	`)
	return err
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
