//go:build it

package ledgerstate_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/formancehq/reconciliation/internal/controlstate/ledgerstate"
	"github.com/formancehq/reconciliation/internal/controlstate/tokenstate"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The benchmark replays one workload on both layouts and measures writes,
// ledger growth and the reads reconciliation needs. Run it with
// RECON_BENCH=1 (and RECON_BENCH_RULES, RECON_BENCH_CYCLES to resize).
//
// Each rule runs cycles of six evaluations, each cycle in a period of its
// own, so resolved alerts accumulate as they do in production:
//
//	pass · fail (open) · fail again · acknowledge · fail again · pass (resolve)

type flavor struct {
	name  string
	store controlstate.ControlState
	// open, again, ack and resolve build the alert part of a unit of work.
	alertWrite func(step string, alert controlstate.Entry) ([]controlstate.Write, []controlstate.Bump)
}

func envInt(name string, def int) int {
	var v int
	if _, err := fmt.Sscan(os.Getenv(name), &v); err != nil || v <= 0 {
		return def
	}
	return v
}

type latencies struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latencies) add(d time.Duration) { l.mu.Lock(); l.d = append(l.d, d); l.mu.Unlock() }
func (l *latencies) pct(p float64) time.Duration {
	if len(l.d) == 0 {
		return 0
	}
	s := slices.Clone(l.d)
	slices.Sort(s)
	return s[min(len(s)-1, int(float64(len(s))*p))]
}
func (l *latencies) String() string {
	return fmt.Sprintf("n=%d p50=%s p95=%s p99=%s", len(l.d), l.pct(.5).Round(10*time.Microsecond), l.pct(.95).Round(10*time.Microsecond), l.pct(.99).Round(10*time.Microsecond))
}

func timeIt(n int, fn func() error) (*latencies, error) {
	l := &latencies{}
	for range n {
		start := time.Now()
		if err := fn(); err != nil {
			return l, err
		}
		l.add(time.Since(start))
	}
	return l, nil
}

func countAccounts(t *testing.T, client *ledger.Client, name string) int {
	n := 0
	require.NoError(t, client.QueryAccountsFunc(context.Background(), name, nil, func(*commonpb.Account) error { n++; return nil }))
	return n
}

func TestIntegration_BenchLayouts(t *testing.T) {
	if os.Getenv("RECON_BENCH") == "" {
		t.Skip("set RECON_BENCH=1")
	}
	rules := envInt("RECON_BENCH_RULES", 40)
	cycles := envInt("RECON_BENCH_CYCLES", 8)
	workers := envInt("RECON_BENCH_WORKERS", 8)
	var report strings.Builder
	fmt.Fprintf(&report, "# Control ledger layout benchmark\n\nledger %s, %d rules × %d cycles × 6 evaluations, %d workers\n\n", ledgerAddr(), rules, cycles, workers)

	// Prototype: one account per version, generic namespace, AUDIT chart.
	tokClient, err := ledger.NewClient(ledgerAddr(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tokClient.Close() })
	tokName := "recon-bench-tok-" + uuid.NewString()[:8]
	require.NoError(t, tokClient.CreateLedger(context.Background(), tokName, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))
	t.Cleanup(func() { _ = tokClient.DeleteLedger(context.Background(), tokName) })
	tok := tokenstate.New(tokClient, tokName)
	require.NoError(t, tok.Provision(context.Background()))

	// RFC 0022 9959818: one account per entity, recon chart, STRICT.
	newStoreV, newClient, newName := newStore(t, ledgerstate.Recon{}, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_STRICT)

	flavors := []flavor{
		{
			name:  "prototype (one account per version)",
			store: tok,
			alertWrite: func(step string, a controlstate.Entry) ([]controlstate.Write, []controlstate.Bump) {
				switch step {
				case "open":
					return []controlstate.Write{{Key: a.Key, Fields: map[string]string{"status": "open"}}}, nil
				case "ack":
					return []controlstate.Write{{Key: a.Key, Fields: map[string]string{"status": "ack"}}}, nil
				case "resolve":
					return []controlstate.Write{{Key: a.Key, Fields: map[string]string{"status": "resolved"}}}, nil
				}
				return nil, nil // a repeat failure writes nothing on the alert
			},
		},
		{
			name:  "RFC 0022 (one account per entity)",
			store: newStoreV,
			alertWrite: func(step string, a controlstate.Entry) ([]controlstate.Write, []controlstate.Bump) {
				switch step {
				case "open":
					return []controlstate.Write{{Key: a.Key, Fields: map[string]string{"status": "open", "rule": strings.SplitN(a.Key.ID, ":", 2)[0]}, Move: map[string]int64{"OPEN": 1, "OCC": 1}}}, nil
				case "again":
					return nil, []controlstate.Bump{{Key: a.Key, Add: map[string]int64{"OCC": 1}, Live: map[string]string{"lastSeenAt": time.Now().UTC().Format(time.RFC3339)}}}
				case "ack":
					return []controlstate.Write{{Key: a.Key, Fields: map[string]string{"status": "ack"}, Move: map[string]int64{"OPEN": -1, "ACK": 1}}}, nil
				case "resolve":
					return []controlstate.Write{{Key: a.Key, Fields: map[string]string{"status": "resolved"}, Move: map[string]int64{"ACK": -1}}}, nil
				}
				return nil, nil
			},
		},
	}

	ruleIDs := make([]string, rules)
	for i := range ruleIDs {
		ruleIDs[i] = uuid.NewString()
	}
	steps := []string{"pass", "open", "again", "ack", "again", "resolve"}

	fmt.Fprintf(&report, "## Writes\n\n| Layout | Commits | Wall time | Commits/s | Commit latency | Accounts after | Accounts per rule |\n|---|---|---|---|---|---|---|\n")
	for _, f := range flavors {
		ctx := context.Background()
		lat := &latencies{}
		var commits atomic.Int64
		jobs := make(chan string)
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		start := time.Now()
		for range workers {
			wg.Go(func() {
				for rid := range jobs {
					if err := runRule(ctx, f, rid, cycles, steps, lat, &commits); err != nil {
						errs <- fmt.Errorf("%s rule %s: %w", f.name, rid, err)
						return
					}
				}
			})
		}
		for _, rid := range ruleIDs {
			jobs <- rid
		}
		close(jobs)
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		wall := time.Since(start)
		var accts int
		if f.store == tok {
			accts = countAccounts(t, tokClient, tokName)
		} else {
			accts = countAccounts(t, newClient, newName)
		}
		fmt.Fprintf(&report, "| %s | %d | %s | %.0f | %s | %d | %.1f |\n", f.name, commits.Load(), wall.Round(time.Millisecond), float64(commits.Load())/wall.Seconds(), lat, accts, float64(accts)/float64(rules))
	}

	// Reads on the RFC 0022 ledger.
	ctx := context.Background()
	const reps = 30
	page := ruleIDs
	r0 := ruleIDs[0]
	last := fmt.Sprintf("p%03d", cycles-1)
	alertKey := controlstate.Key{Kind: ledgerstate.KindAlert, ID: ledgerstate.AlertID(r0, last, "9c4e1a7b3f20d5e8")}
	keys := []controlstate.Key{{Kind: ledgerstate.KindRule, ID: r0}, {Kind: ledgerstate.KindEval, ID: r0}, alertKey}

	fmt.Fprintf(&report, "\n## Reads on the RFC 0022 layout (%d repetitions each)\n\n| Read | Call | Latency | Result |\n|---|---|---|---|\n", reps)
	row := func(read, call string, l *latencies, result string) {
		fmt.Fprintf(&report, "| %s | %s | %s | %s |\n", read, call, l, result)
	}

	var snap controlstate.Snapshot
	l, err := timeIt(reps, func() (err error) { snap, err = newStoreV.Load(ctx, keys...); return })
	require.NoError(t, err)
	row("Load a unit of work (rule, counter, alert)", "1 ListAccounts, or of 3 exact addresses", l, fmt.Sprintf("eval v%d, alert v%d %v", snap.Get(keys[1]).Version, snap.Get(keys[2]).Version, snap.Get(keys[2]).Balances))

	l, err = timeIt(reps, func() error {
		for _, k := range keys {
			addr, _ := ledgerstate.Recon{}.Address(k)
			if _, err := newClient.GetAccount(ctx, newName, addr); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
	row("Load a unit of work (rule, counter, alert)", "3 GetAccount", l, "")

	var gauges int
	l, err = timeIt(reps, func() error {
		gauges = 0
		return newClient.QueryAccountsFunc(ctx, newName, ledgerschema.FilterAddressPrefix("pool:rule:"), func(*commonpb.Account) error { gauges++; return nil })
	})
	require.NoError(t, err)
	row("Counts for a page of rules", fmt.Sprintf("ListAccounts on pool:rule: (%d gauges)", gauges), l, "open/ack/accepted/occurrences per rule")

	prefixes := make([]string, 0, len(page))
	for _, r := range page {
		prefixes = append(prefixes, "alert:rule:"+r+":")
	}
	active := ledgerschema.FilterAll(ledgerschema.FilterAddressPrefix("alert:rule:"), ledgerschema.FilterAny(ledgerschema.FilterMetadataString("status", "open"), ledgerschema.FilterMetadataString("status", "ack")))
	l, err = timeIt(reps, func() error {
		_, err := newClient.AggregateVolumesGrouped(ctx, newName, active, prefixes)
		return err
	})
	require.NoError(t, err)
	row("Counts for a page of rules", "AggregateVolumes grouped, status filter (active alerts only)", l, "")

	l, err = timeIt(reps, func() error {
		_, err := newClient.AggregateVolumesGrouped(ctx, newName, ledgerschema.FilterAddressPrefix("alert:rule:"), prefixes)
		return err
	})
	require.NoError(t, err)
	alerts := 0
	require.NoError(t, newClient.QueryAccountsFunc(ctx, newName, ledgerschema.FilterAddressPrefix("alert:rule:"), func(*commonpb.Account) error { alerts++; return nil }))
	row("Counts for a page of rules", fmt.Sprintf("AggregateVolumes grouped, address prefix only (scans all %d alerts)", alerts), l, "")

	var gauge *commonpb.Account
	l, err = timeIt(reps, func() (err error) { gauge, err = newClient.GetAccount(ctx, newName, "pool:rule:"+r0); return })
	require.NoError(t, err)
	row("Counts for one rule", "GetAccount on its gauge", l, volumesString(gauge))

	l, err = timeIt(reps, func() error {
		_, err := newClient.AggregateVolumes(ctx, newName, ledgerschema.FilterAll(ledgerschema.FilterMetadataString("period", last), ledgerschema.FilterAny(ledgerschema.FilterMetadataString("status", "open"), ledgerschema.FilterMetadataString("status", "ack"))))
		return err
	})
	require.NoError(t, err)
	row("Is the last period green, over all rules?", "AggregateVolumes, period + status filter", l, "")

	var txs int
	l, err = timeIt(reps, func() error {
		txs = 0
		stream, err := newClient.Service().ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: newName, Options: &commonpb.ListOptions{PageSize: 100, Filter: ledgerschema.FilterMetadataString("rule", r0)}})
		if err != nil {
			return err
		}
		for {
			if _, err := stream.Recv(); err != nil {
				break
			}
			txs++
		}
		return nil
	})
	require.NoError(t, err)
	row("Timeline of one rule, first page", "ListTransactions metadata[rule], page 100", l, fmt.Sprintf("%d transactions", txs))

	out := report.String()
	t.Log("\n" + out)
	if path := os.Getenv("RECON_BENCH_OUT"); path != "" {
		require.NoError(t, os.WriteFile(path, []byte(out), 0o644))
	}
}

func runRule(ctx context.Context, f flavor, rid string, cycles int, steps []string, lat *latencies, commits *atomic.Int64) error {
	evalKey := controlstate.Key{Kind: ledgerstate.KindEval, ID: rid}
	if _, ok := f.store.(*tokenstate.Store); ok {
		evalKey = controlstate.Key{Kind: "token", ID: rid}
	}
	commit := func(uow controlstate.UnitOfWork) error {
		start := time.Now()
		_, err := f.store.Commit(ctx, uow)
		lat.add(time.Since(start))
		commits.Add(1)
		return err
	}
	// Create the rule.
	ruleKey := controlstate.Key{Kind: ledgerstate.KindRule, ID: rid}
	if err := commit(controlstate.UnitOfWork{
		IdempotencyKey: "create-" + rid,
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(ruleKey)},
		Put:            []controlstate.Write{{Key: ruleKey, Fields: map[string]string{"template": "balance_bounds", "enabled": "true", "spec": strings.Repeat("x", 600)}}},
	}); err != nil {
		return err
	}
	for c := range cycles {
		period := fmt.Sprintf("p%03d", c)
		alertKey := controlstate.Key{Kind: ledgerstate.KindAlert, ID: ledgerstate.AlertID(rid, period, "9c4e1a7b3f20d5e8")}
		for i, step := range steps {
			snap, err := f.store.Load(ctx, evalKey, alertKey)
			if err != nil {
				return err
			}
			ev, al := snap.Get(evalKey), snap.Get(alertKey)
			var uow controlstate.UnitOfWork
			if step == "ack" {
				// An operator action: only the alert.
				w, _ := f.alertWrite(step, al)
				uow = controlstate.UnitOfWork{
					IdempotencyKey: fmt.Sprintf("%s-%d-%d", rid, c, i),
					Expect:         []controlstate.Precondition{controlstate.Expect(al)},
					Put:            w,
					Records:        []controlstate.Record{{Kind: "activity", Subject: alertKey, Fields: map[string]string{"action": "ack"}}},
				}
			} else {
				evExpect := controlstate.Expect(ev)
				uow = controlstate.UnitOfWork{
					IdempotencyKey: fmt.Sprintf("%s-%d-%d", rid, c, i),
					Expect:         []controlstate.Precondition{evExpect},
					Put:            []controlstate.Write{{Key: evalKey, Fields: map[string]string{"lastVerdict": step}}},
					Records:        []controlstate.Record{{Kind: "capture", Subject: evalKey, Fields: map[string]string{"verdict": step, "evidence": strings.Repeat("e", 400), "period": period}}},
				}
				w, b := f.alertWrite(step, al)
				if len(w) > 0 {
					if step == "open" {
						uow.Expect = append(uow.Expect, controlstate.ExpectAbsent(alertKey))
					} else {
						uow.Expect = append(uow.Expect, controlstate.Expect(al))
					}
					uow.Put = append(uow.Put, w...)
				}
				uow.Bumps = b
			}
			if err := commit(uow); err != nil {
				return fmt.Errorf("cycle %d step %s: %w", c, step, err)
			}
		}
	}
	return nil
}

func volumesString(a *commonpb.Account) string {
	var parts []string
	for _, v := range a.GetVolumes() {
		b, err := v.GetVolumes().GetBalance().ToBigInt()
		if err != nil {
			continue
		}
		parts = append(parts, v.GetAsset()+" "+b.String())
	}
	return strings.Join(parts, ", ")
}
