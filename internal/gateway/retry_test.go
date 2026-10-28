package gateway

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetrySchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t)
		_, key := provision(t, s, "1")
		ctx := context.Background()
		r, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "reported"}, time.Now(), testAccessConfig())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(ctx, r.ID, "completed", ""); err != nil {
			t.Fatal(err)
		}
		// Advance the clock across each persisted deadline, including the cap.
		for attempt, delay := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 320 * time.Second, 640 * time.Second, 1280 * time.Second, 2560 * time.Second, time.Hour, time.Hour} {
			if err := s.Retry(ctx, r, 12); err != nil {
				t.Fatal(err)
			}
			r, err = s.Request(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Attempts != attempt+1 {
				t.Fatalf("attempts=%d, want %d", r.Attempts, attempt+1)
			}
			time.Sleep(delay - time.Second)
			pending, err := s.Pending(ctx, 25)
			if err != nil || len(pending) != 0 {
				t.Fatalf("retry eligible early: %d requests, %v", len(pending), err)
			}
			time.Sleep(time.Second)
			pending, err = s.Pending(ctx, 25)
			if err != nil || len(pending) != 1 || pending[0].ID != r.ID {
				t.Fatalf("retry missing at deadline: %+v, %v", pending, err)
			}
		}
		if err := s.Retry(ctx, r, 12); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Hour)
		pending, err := s.Pending(ctx, 25)
		if err != nil || len(pending) != 0 {
			t.Fatalf("exhausted retry still eligible: %+v, %v", pending, err)
		}
		r, err = s.Request(ctx, r.ID)
		if err != nil || r.Accounting != "unresolved" || r.Attempts != 12 {
			t.Fatalf("exhausted retry: %+v, %v", r, err)
		}
	})
}
