package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// purgeList asks whether listing an EPHEMERAL hold prefix costs O(open holds)
// or O(holds ever created): the daily stock lists the open holds live (§4),
// and a lettering ledger purges every hold it letters. It opens -open holds
// that stay open, then, stage by stage, books holds opened and drained in the
// same batch (so purged), and after each stage lists the prefix and aggregates
// it, both live.
func purgeList(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("purge-list", flag.ExitOnError)
	ledger := fs.String("ledger", "purge", "")
	open := fs.Uint64("open", 10_000, "holds that stay open")
	stagesArg := fs.String("stages", "0,100000,500000,1000000", "cumulative purged holds after each stage")
	batch := fs.Int("batch", 250, "holds per Apply batch (two transactions each)")
	workers := fs.Int("workers", 16, "")
	reps := fs.Int("reps", 3, "")
	_ = fs.Parse(args)
	const prefix = "hold:"

	_, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
		Name: *ledger, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
		AccountTypes: map[string]*commonpb.AccountType{"hold": {Name: "hold", Pattern: prefix + "{id}", Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL}},
	}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create ledger: %v", err)
	}
	// book runs n holds starting at from through the workers: open only, or
	// open and drain in the same batch.
	book := func(from, n uint64, drain bool, tag string) {
		jobs := make(chan [2]uint64, *workers*2)
		var wg sync.WaitGroup
		for w := 0; w < *workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range jobs {
					var reqs []*servicepb.Request
					for i := r[0]; i < r[1]; i++ {
						h := fmt.Sprintf("%s%s%d", prefix, tag, i)
						reqs = append(reqs, createTx(*ledger, nil, usd("world", h, amount(i))))
						if drain {
							reqs = append(reqs, createTx(*ledger, nil, usd(h, "psp:main", amount(i))))
						}
					}
					for attempt := 0; ; attempt++ {
						if _, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}}); err == nil {
							break
						} else if attempt > 20 {
							log.Fatalf("apply: %v", err)
						}
						time.Sleep(200 * time.Millisecond)
					}
				}
			}()
		}
		for i := from; i < from+n; i += uint64(*batch) {
			jobs <- [2]uint64{i, min(i+uint64(*batch), from+n)}
		}
		close(jobs)
		wg.Wait()
	}
	t0 := time.Now()
	book(0, *open, false, "open-")
	fmt.Printf("ledger %s: %d open holds booked in %s\n", *ledger, *open, time.Since(t0).Round(time.Millisecond))
	fmt.Printf("\n  %10s  %10s  %20s  %20s\n", "purged", "listed", "ListAccounts, 1 stream", "AggregateVolumes")
	purged := uint64(0)
	for _, st := range strings.Split(*stagesArg, ",") {
		var target uint64
		fmt.Sscan(st, &target)
		if target > purged {
			t0 := time.Now()
			book(purged, target-purged, true, "p-")
			log.Printf("purged %d more holds in %s", target-purged, time.Since(t0).Round(time.Second))
			purged = target
		}
		var bestList, bestAgg time.Duration
		n := 0
		for r := 0; r < *reps; r++ {
			t0 := time.Now()
			c, _, _, err := scan(ctx, *ledger, prefix, 0, 1000, func(row) {})
			if err != nil {
				log.Fatalf("scan: %v", err)
			}
			if el := time.Since(t0); bestList == 0 || el < bestList {
				bestList = el
			}
			n = c
			t0 = time.Now()
			if _, err := svc.AggregateVolumes(ctx, &servicepb.AggregateVolumesRequest{Ledger: *ledger, Filter: prefixFilter(prefix), CollapseColors: true}); err != nil {
				log.Fatalf("aggregate: %v", err)
			}
			if el := time.Since(t0); bestAgg == 0 || el < bestAgg {
				bestAgg = el
			}
		}
		fmt.Printf("  %10d  %10d  %20s  %20s\n", purged, n, bestList.Round(time.Millisecond), bestAgg.Round(time.Millisecond))
	}
}

// watchCmd times the metadata watch as ADR-005 §5 runs it: every log of the
// window (lo, hi], unfiltered, over K log-id ranges, counting the payload kinds
// and the metadata writes on transactions, with no fold.
func watchCmd(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	ledger := fs.String("ledger", "cut10m", "")
	last := fs.Uint64("last", 1_000_000, "logs up to the head")
	rangesArg := fs.String("ranges", "1,8,16", "")
	reps := fs.Int("reps", 2, "")
	_ = fs.Parse(args)
	head, _ := ledgerStats(ctx, *ledger)
	lo := head - min(head, *last)
	fmt.Printf("ledger %s, logs (%d,%d], best of %d\n", *ledger, lo, head, *reps)
	for _, ks := range strings.Split(*rangesArg, ",") {
		var k int
		fmt.Sscan(ks, &k)
		var best time.Duration
		var counts map[string]uint64
		for r := 0; r < *reps; r++ {
			c, el := readLogs(ctx, *ledger, lo, head, k)
			if best == 0 || el < best {
				best = el
			}
			counts = c
		}
		var total uint64
		for _, v := range counts {
			total += v
		}
		fmt.Printf("  K=%-3d %8d logs  %8s  %7.0f logs/s  %v\n", k, total, best.Round(time.Millisecond), float64(total)/best.Seconds(), counts)
	}
}

// readLogs streams the logs of (lo, hi] over k ranges and counts them by kind.
func readLogs(ctx context.Context, ledger string, lo, hi uint64, k int) (map[string]uint64, time.Duration) {
	span := (hi - lo + uint64(k) - 1) / uint64(k)
	var created, reverted, txMeta, other atomic.Uint64
	var wg sync.WaitGroup
	t0 := time.Now()
	for r := 0; r < k; r++ {
		rlo := lo + uint64(r)*span
		rhi := min(rlo+span, hi)
		if rlo >= rhi {
			continue
		}
		wg.Add(1)
		go func(rlo, rhi uint64) {
			defer wg.Done()
			cond := &commonpb.UintCondition{Min: &rlo, MinExclusive: true, Max: &rhi}
			var cursor string
			for {
				stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor,
					Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_LogId{LogId: &commonpb.LogIdCondition{Cond: cond}}}}})
				if err != nil {
					log.Fatalf("list logs: %v", err)
				}
				for {
					l, rerr := stream.Recv()
					if errors.Is(rerr, io.EOF) {
						break
					}
					if rerr != nil {
						log.Fatalf("recv log: %v", rerr)
					}
					d := l.GetPayload().GetApply().GetLog().GetData()
					switch {
					case d.GetCreatedTransaction() != nil:
						created.Add(1)
					case d.GetRevertedTransaction() != nil:
						reverted.Add(1)
					case d.GetSavedMetadata().GetTarget().GetTransactionId() != 0:
						txMeta.Add(1)
					default:
						other.Add(1)
					}
				}
				cursor = ""
				if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
					cursor = vals[0]
				}
				if cursor == "" {
					return
				}
			}
		}(rlo, rhi)
	}
	wg.Wait()
	return map[string]uint64{"created": created.Load(), "reverted": reverted.Load(), "tx-metadata": txMeta.Load(), "other": other.Load()}, time.Since(t0)
}

// iatCheck checks the two assumptions the cut rests on (§3): a transaction's
// inserted_at equals the date of the log that created it (so S and T name the
// same instant), and inserted_at never goes down as the transaction id goes up
// (so a transaction inserted after the cut-off always lands after T). It reads
// every log and every transaction of the ledger in id order.
func iatCheck(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("iat-check", flag.ExitOnError)
	ledger := fs.String("ledger", "cut10m", "")
	_ = fs.Parse(args)
	headLog, headTx := ledgerStats(ctx, *ledger)
	t0 := time.Now()
	logDate := make(map[uint64]uint64, headTx) // tx id -> date of its creating log
	var cursor string
	for {
		stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor}})
		if err != nil {
			log.Fatal(err)
		}
		for {
			l, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				log.Fatal(rerr)
			}
			ll := l.GetPayload().GetApply().GetLog()
			d := ll.GetData()
			tx := d.GetCreatedTransaction().GetTransaction()
			if tx == nil {
				tx = d.GetRevertedTransaction().GetRevertTransaction()
			}
			if tx != nil {
				logDate[tx.GetId()] = ll.GetDate().GetData()
			}
		}
		cursor = ""
		if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
			cursor = vals[0]
		}
		if cursor == "" {
			break
		}
	}
	var n, mismatch, backwards, missing uint64
	var prev uint64
	var firstMismatch string
	cursor = ""
	for {
		stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor, Reverse: true}})
		if err != nil {
			log.Fatal(err)
		}
		for {
			tx, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				log.Fatal(rerr)
			}
			n++
			iat := tx.GetInsertedAt().GetData()
			ld, ok := logDate[tx.GetId()]
			switch {
			case !ok:
				missing++
			case ld != iat:
				mismatch++
				if firstMismatch == "" {
					firstMismatch = fmt.Sprintf("tx %d: inserted_at %d, log date %d", tx.GetId(), iat, ld)
				}
			}
			if iat < prev {
				backwards++
			}
			prev = iat
		}
		cursor = ""
		if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
			cursor = vals[0]
		}
		if cursor == "" {
			break
		}
	}
	fmt.Printf("ledger %s: %d logs, %d transactions read in %s\n", *ledger, headLog, n, time.Since(t0).Round(time.Second))
	fmt.Printf("  inserted_at = date of the creating log: %d mismatches, %d transactions with no creating log %s\n", mismatch, missing, firstMismatch)
	fmt.Printf("  inserted_at going down as the id goes up: %d times\n", backwards)
}
