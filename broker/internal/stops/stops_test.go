package stops_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/example-org/workspace/broker/internal/stops"
)

// Every Store behaves the same. The Postgres store runs only when
// BROKER_TEST_DATABASE_URL points at a database (the local stack provides one).
func stores(t *testing.T) map[string]stops.Store {
	t.Helper()
	out := map[string]stops.Store{"memory": stops.NewMemory()}
	if url := os.Getenv("BROKER_TEST_DATABASE_URL"); url != "" {
		p, err := stops.NewPostgres(context.Background(), url)
		if err != nil {
			t.Fatalf("postgres: %v", err)
		}
		t.Cleanup(p.Close)
		out["postgres"] = p
	}
	return out
}

func TestStopAndClear(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			agent := "test-" + name
			_ = s.Clear(ctx, agent, "test")

			if _, stopped, err := s.Stopped(ctx, agent); err != nil || stopped {
				t.Fatalf("new agent: stopped=%v err=%v", stopped, err)
			}
			if err := s.Set(ctx, stops.Stop{Agent: agent, By: "alex", Reason: "test"}); err != nil {
				t.Fatal(err)
			}
			got, stopped, err := s.Stopped(ctx, agent)
			if err != nil || !stopped || got.By != "alex" || got.Reason != "test" {
				t.Fatalf("after stop: %+v stopped=%v err=%v", got, stopped, err)
			}
			if err := s.Clear(ctx, agent, "alex"); err != nil {
				t.Fatal(err)
			}
			if _, stopped, _ := s.Stopped(ctx, agent); stopped {
				t.Fatal("still stopped after clear")
			}
		})
	}
}

// TM-R1: who stopped an agent, and who started it again, stays on record.
func TestHistoryRecordsWhoStoppedAndWhoStarted(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			agent := "history-" + name + "-" + time.Now().Format("150405.000000")
			if err := s.Set(ctx, stops.Stop{Agent: agent, By: "jane", Reason: "odd pushes"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Clear(ctx, agent, "max"); err != nil {
				t.Fatal(err)
			}
			got, err := s.History(ctx, agent)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 ||
				got[0].Action != stops.ActionStop || got[0].By != "jane" || got[0].Reason != "odd pushes" ||
				got[1].Action != stops.ActionStart || got[1].By != "max" {
				t.Fatalf("history: %+v", got)
			}
			if got[0].At.IsZero() || got[1].At.Before(got[0].At) {
				t.Fatalf("history times: %+v", got)
			}
		})
	}
}

func TestStartingAnAgentThatIsNotStoppedRecordsNothing(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			agent := "running-" + name + "-" + time.Now().Format("150405.000000")
			if err := s.Clear(ctx, agent, "max"); err != nil {
				t.Fatal(err)
			}
			if got, err := s.History(ctx, agent); err != nil || len(got) != 0 {
				t.Fatalf("history: %+v err=%v", got, err)
			}
		})
	}
}

func TestStopAndStartNeedAName(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			if err := s.Set(ctx, stops.Stop{Agent: "nameless-" + name, Reason: "test"}); !errors.Is(err, stops.ErrNoName) {
				t.Fatalf("stop without a name: %v", err)
			}
			if err := s.Clear(ctx, "nameless-"+name, ""); !errors.Is(err, stops.ErrNoName) {
				t.Fatalf("start without a name: %v", err)
			}
		})
	}
}
