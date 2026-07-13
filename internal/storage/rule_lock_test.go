package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestWithRuleLock_SerialisesSameRule is the core guarantee behind the
// stale-evaluation-ordering fix: two evaluations of the SAME rule may never run
// their read+persist windows concurrently. Many goroutines take the lock for
// one rule id and record the peak number inside the critical section at once;
// with the advisory lock that peak must be exactly 1.
func TestWithRuleLock_SerialisesSameRule(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID := uuid.New()

	const goroutines = 4
	var (
		mu        sync.Mutex
		inside    int
		maxInside int
		wg        sync.WaitGroup
		start     = make(chan struct{})
	)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.WithRuleLock(ctx, ruleID, func(ctx context.Context) error {
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()
				// Hold the section long enough that any missing mutual exclusion
				// would show up as an overlap.
				time.Sleep(40 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			require.NoError(t, err)
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, 1, maxInside, "evaluations of the same rule must not overlap")
}

// TestWithRuleLock_DifferentRulesDoNotContend proves the lock is scoped per
// rule, not a global bottleneck (and that the serialisation above is the
// advisory lock, not the connection pool): two different rule ids must be able
// to hold their locks at the same time.
func TestWithRuleLock_DifferentRulesDoNotContend(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	run := func(ruleID uuid.UUID) {
		go func() {
			_ = s.WithRuleLock(ctx, ruleID, func(ctx context.Context) error {
				entered <- struct{}{}
				<-release // hold the lock until both goroutines are inside
				return nil
			})
		}()
	}
	run(uuid.New())
	run(uuid.New())

	// Both must enter concurrently. If the locks contended (a regression to a
	// global lock), only one would enter and this would time out.
	deadline := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatalf("different rules contended on the lock: only %d of 2 entered", i)
		}
	}
	close(release)
}
