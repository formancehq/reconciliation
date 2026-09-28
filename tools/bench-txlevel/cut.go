package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// cutCost measures what resolving a cut costs as the cut-off ages (ADR-005
// decision 24). The ledger materializes a date range before paging it
// (query/compile.go compileTimestampRangeCondition), so `date > cut-off` with no
// upper bound reads every index entry written since the cut-off, while a
// bounded `cut-off < date ≤ cut-off + δ` reads only δ's worth.
//
// With -load, it first books -n light transactions on a fresh ledger with no
// date index, then creates the log-date (lldt) and inserted_at (txiat) indexes
// on that history and times how long each takes to serve. It then resolves S
// (ListLogs on the log date) and T (ListTransactions on inserted_at, reverse)
// for cut-offs with a growing number of entries after them, unbounded and
// bounded, and checks each answer against the id it must return.
func cutCost(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("cut-cost", flag.ExitOnError)
	ledger := fs.String("ledger", "cut", "")
	load := fs.Bool("load", false, "create the ledger and book -n transactions first")
	n := fs.Uint64("n", 10_000_000, "")
	batch := fs.Int("batch", 1000, "")
	workers := fs.Int("workers", 16, "")
	reps := fs.Int("reps", 3, "")
	_ = fs.Parse(args)

	if *load {
		ensureLedger(ctx, *ledger)
		jobs := make(chan int, *workers*2)
		var done atomic.Uint64
		var wg sync.WaitGroup
		t0 := time.Now()
		for w := 0; w < *workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for size := range jobs {
					reqs := make([]*servicepb.Request, 0, size)
					for i := 0; i < size; i++ {
						reqs = append(reqs, createTx(*ledger, nil, usd("world", fmt.Sprintf("internal:%d", i%1000), 1)))
					}
					for attempt := 0; ; attempt++ {
						if _, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}}); err == nil {
							break
						} else if attempt > 20 {
							log.Fatalf("apply: %v", err)
						}
						time.Sleep(200 * time.Millisecond)
					}
					if d := done.Add(uint64(size)); d%1_000_000 < uint64(size) {
						log.Printf("%s: %d tx (%.0f tx/s)", *ledger, d, float64(d)/time.Since(t0).Seconds())
					}
				}
			}()
		}
		for left := *n; left > 0; {
			size := min(uint64(*batch), left)
			jobs <- int(size)
			left -= size
		}
		close(jobs)
		wg.Wait()
		fmt.Printf("loaded %d tx on %s in %s\n", done.Load(), *ledger, time.Since(t0).Round(time.Second))

		for _, ix := range []struct {
			name string
			id   *commonpb.IndexID
			try  func() error
		}{
			{"log date (lldt)", &commonpb.IndexID{Kind: &commonpb.IndexID_LogBuiltin{LogBuiltin: commonpb.LogBuiltinIndex_LOG_BUILTIN_INDEX_DATE}},
				func() error { _, _, err := firstLogAfter(ctx, *ledger, 0, 1); return err }},
			{"inserted_at (txiat)", &commonpb.IndexID{Kind: &commonpb.IndexID_TxBuiltin{TxBuiltin: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_INSERTED_AT}},
				func() error { _, _, err := firstTxAfter(ctx, *ledger, 0, 1); return err }},
		} {
			t0 := time.Now()
			if _, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{Ledger: *ledger, Id: ix.id}}}); err != nil && status.Code(err) != codes.AlreadyExists {
				log.Fatalf("create index %s: %v", ix.name, err)
			}
			for {
				err := ix.try()
				if err == nil || errors.Is(err, errNoEntry) {
					break // serves: an empty range answers without Unavailable
				}
				if status.Code(err) != codes.Unavailable {
					log.Fatalf("index %s: %v", ix.name, err)
				}
				time.Sleep(250 * time.Millisecond)
			}
			fmt.Printf("index %s on %d transactions: serves after %s\n", ix.name, *n, time.Since(t0).Round(100*time.Millisecond))
		}
	}

	headLog, headTx := ledgerStats(ctx, *ledger)
	logDate := func(id uint64) uint64 {
		stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1,
			Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_LogId{LogId: &commonpb.LogIdCondition{Cond: &commonpb.UintCondition{Min: &id, Max: &id}}}}}})
		if err != nil {
			log.Fatal(err)
		}
		l, err := stream.Recv()
		if err != nil {
			log.Fatalf("log %d: %v", id, err)
		}
		return l.GetPayload().GetApply().GetLog().GetDate().GetData()
	}
	txDate := func(id uint64) uint64 {
		stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1,
			Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
				Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID, Cond: &commonpb.UintCondition{Min: &id, Max: &id}}}}}})
		if err != nil {
			log.Fatal(err)
		}
		tx, err := stream.Recv()
		if err != nil {
			log.Fatalf("tx %d: %v", id, err)
		}
		return tx.GetInsertedAt().GetData()
	}
	// The date unit, from the span of the transactions' insertion dates.
	firstD, lastD := txDate(1), txDate(headTx)
	perEntry := float64(lastD-firstD) / float64(headTx-1)
	fmt.Printf("\nledger %s: %d logs, %d transactions; one entry per %.1f date units on average\n", *ledger, headLog, headTx, perEntry)

	timeIt := func(f func() (uint64, error)) (uint64, time.Duration, time.Duration) {
		var got uint64
		var first, best time.Duration
		for r := 0; r < *reps; r++ {
			t0 := time.Now()
			id, err := f()
			el := time.Since(t0)
			if err != nil {
				log.Fatal(err)
			}
			got = id
			if r == 0 {
				first = el
			}
			if best == 0 || el < best {
				best = el
			}
		}
		return got, first, best
	}
	fmt.Println("\nentries after the cut-off: unbounded `date > cut-off` against bounded `cut-off < date ≤ cut-off + δ` (δ from ≈ 1,000 entries, doubled while empty), first run / best of", *reps)
	fmt.Printf("  %10s  %-6s  %21s  %21s  %s\n", "after", "read", "unbounded", "bounded", "answer")
	delta := uint64(math.Max(1, 1000*perEntry))
	for _, after := range []uint64{1_000, 10_000, 100_000, 1_000_000, 5_000_000, headTx - 1} {
		if after >= headTx {
			continue
		}
		// The cut-off is the date of entry head-after; the answer is right when
		// date(S) <= cut-off < date(S+1), whichever batch shares a date.
		cut := logDate(headLog - after)
		s1, uf, ub := timeIt(func() (uint64, error) { id, _, err := firstLogAfter(ctx, *ledger, cut, 0); return id - 1, err })
		s2, bf, bb := timeIt(func() (uint64, error) {
			id, err := widening(func(d uint64) (uint64, error) { id, _, err := firstLogAfter(ctx, *ledger, cut, cut+d); return id, err }, delta)
			return id - 1, err
		})
		okS := s1 == s2 && logDate(s1) <= cut && logDate(s1+1) > cut
		fmt.Printf("  %10d  %-6s  %9s / %9s  %9s / %9s  S=%d, %d logs after it, right: %v\n", after, "S", uf.Round(time.Millisecond), ub.Round(time.Millisecond), bf.Round(time.Millisecond), bb.Round(time.Millisecond), s1, headLog-s1, okS)
		cut = txDate(headTx - after)
		t1, uf, ub := timeIt(func() (uint64, error) { id, _, err := firstTxAfter(ctx, *ledger, cut, 0); return id - 1, err })
		t2, bf, bb := timeIt(func() (uint64, error) {
			id, err := widening(func(d uint64) (uint64, error) { id, _, err := firstTxAfter(ctx, *ledger, cut, cut+d); return id, err }, delta)
			return id - 1, err
		})
		okT := t1 == t2 && txDate(t1) <= cut && txDate(t1+1) > cut
		fmt.Printf("  %10d  %-6s  %9s / %9s  %9s / %9s  T=%d, %d transactions after it, right: %v\n", after, "T", uf.Round(time.Millisecond), ub.Round(time.Millisecond), bf.Round(time.Millisecond), bb.Round(time.Millisecond), t1, headTx-t1, okT)
	}
}

var errNoEntry = errors.New("no entry in the range")

// widening runs a bounded first-after read, doubling its width δ while the range
// is empty, as the cut does (design doc §3).
func widening(read func(delta uint64) (uint64, error), delta uint64) (uint64, error) {
	for {
		id, err := read(delta)
		if !errors.Is(err, errNoEntry) {
			return id, err
		}
		if delta > 1<<50 {
			return 0, err
		}
		delta *= 2
	}
}

// firstLogAfter returns the id of the first log dated after cutoff (and at or
// before upper, when upper is not 0): one page of one row.
func firstLogAfter(ctx context.Context, ledger string, cutoff, upper uint64) (uint64, uint64, error) {
	cond := &commonpb.UintCondition{Min: &cutoff, MinExclusive: true}
	if upper != 0 {
		cond.Max = &upper
	}
	stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1,
		Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_LogBuiltinUint{LogBuiltinUint: &commonpb.LogBuiltinUintCondition{
			Field: commonpb.LogBuiltinIndex_LOG_BUILTIN_INDEX_DATE, Cond: cond}}}}})
	if err != nil {
		return 0, 0, err
	}
	l, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return 0, 0, errNoEntry
	}
	if err != nil {
		return 0, 0, err
	}
	ll := l.GetPayload().GetApply().GetLog()
	return ll.GetId(), ll.GetDate().GetData(), nil
}

// firstTxAfter returns the id of the first transaction inserted after cutoff
// (and at or before upper, when upper is not 0), reading in id order.
func firstTxAfter(ctx context.Context, ledger string, cutoff, upper uint64) (uint64, uint64, error) {
	cond := &commonpb.UintCondition{Min: &cutoff, MinExclusive: true}
	if upper != 0 {
		cond.Max = &upper
	}
	stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1, Reverse: true,
		Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
			Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_INSERTED_AT, Cond: cond}}}}})
	if err != nil {
		return 0, 0, err
	}
	tx, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return 0, 0, errNoEntry
	}
	if err != nil {
		return 0, 0, err
	}
	return tx.GetId(), tx.GetInsertedAt().GetData(), nil
}
