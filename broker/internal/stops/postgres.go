package stops

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the Store the broker runs with. The broker reads it on every
// request, so a stop takes effect on the agent's next call.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to stops database: %w", err)
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS agent_stops (
			agent      text PRIMARY KEY,
			stopped_by text NOT NULL,
			reason     text NOT NULL,
			stopped_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TABLE IF NOT EXISTS agent_stop_events (
			id     bigserial PRIMARY KEY,
			agent  text NOT NULL,
			action text NOT NULL CHECK (action IN ('stop', 'start')),
			by     text NOT NULL,
			reason text NOT NULL DEFAULT '',
			at     timestamptz NOT NULL DEFAULT clock_timestamp()
		);
		CREATE INDEX IF NOT EXISTS agent_stop_events_agent ON agent_stop_events (agent, id);
		-- The history is a record: rows are added, never changed or removed.
		CREATE OR REPLACE FUNCTION agent_stop_events_append_only() RETURNS trigger
			LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'agent_stop_events is append-only'; END $$;
		CREATE OR REPLACE TRIGGER agent_stop_events_append_only
			BEFORE UPDATE OR DELETE ON agent_stop_events
			FOR EACH ROW EXECUTE FUNCTION agent_stop_events_append_only()`)
	if err != nil {
		return fmt.Errorf("create stop tables: %w", err)
	}
	return nil
}

func (p *Postgres) Stopped(ctx context.Context, agent string) (Stop, bool, error) {
	s := Stop{Agent: agent}
	err := p.pool.QueryRow(ctx, `SELECT stopped_by, reason, stopped_at FROM agent_stops WHERE agent = $1`, agent).
		Scan(&s.By, &s.Reason, &s.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stop{}, false, nil
	}
	if err != nil {
		return Stop{}, false, fmt.Errorf("read stop for %s: %w", agent, err)
	}
	return s, true, nil
}

// Set stops the agent and records the stop, in one transaction.
func (p *Postgres) Set(ctx context.Context, s Stop) error {
	if s.By == "" {
		return ErrNoName
	}
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_stops (agent, stopped_by, reason) VALUES ($1, $2, $3)
			ON CONFLICT (agent) DO UPDATE SET stopped_by = $2, reason = $3, stopped_at = now()`, s.Agent, s.By, s.Reason); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO agent_stop_events (agent, action, by, reason) VALUES ($1, 'stop', $2, $3)`, s.Agent, s.By, s.Reason)
		return err
	})
	if err != nil {
		return fmt.Errorf("stop %s: %w", s.Agent, err)
	}
	return nil
}

// Clear starts the agent again and records who did it, in one transaction. An
// agent that is not stopped stays as it is, and nothing is recorded.
func (p *Postgres) Clear(ctx context.Context, agent, by string) error {
	if by == "" {
		return ErrNoName
	}
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM agent_stops WHERE agent = $1`, agent)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO agent_stop_events (agent, action, by) VALUES ($1, 'start', $2)`, agent, by)
		return err
	})
	if err != nil {
		return fmt.Errorf("clear stop for %s: %w", agent, err)
	}
	return nil
}

func (p *Postgres) History(ctx context.Context, agent string) ([]Event, error) {
	rows, err := p.pool.Query(ctx, `SELECT action, by, reason, at FROM agent_stop_events WHERE agent = $1 ORDER BY id`, agent)
	if err != nil {
		return nil, fmt.Errorf("read history of %s: %w", agent, err)
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		e := Event{Agent: agent}
		return e, r.Scan(&e.Action, &e.By, &e.Reason, &e.At)
	})
	if err != nil {
		return nil, fmt.Errorf("read history of %s: %w", agent, err)
	}
	return events, nil
}
