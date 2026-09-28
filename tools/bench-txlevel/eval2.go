package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
)

// flowWrites asks what a filtered flow read costs while the ledger writes: a
// read filtered on metadata goes through the read index, which must be aligned
// with the snapshot it reads (query/aligned_snapshot.go), while writes keep the
// index busy. It reads the product membership, membership first, over the fixed
// window (0, T] taken before any write: idle, then while -writers write keyed
// product transactions to the same ledger, then to another ledger. It reports
// the read time, its slowest page, the rows (which must not change) and the
// writers' rate.
func flowWrites(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("flow-writes", flag.ExitOnError)
	ledger := fs.String("ledger", "product-n9", "")
	other := fs.String("other", "wload", "another ledger of the node, for the writes elsewhere")
	writers := fs.Int("writers", 8, "")
	batch := fs.Int("batch", 100, "transactions per writer Apply")
	ranges := fs.Int("ranges", 8, "")
	reps := fs.Int("reps", 3, "")
	_ = fs.Parse(args)
	_, T := ledgerStats(ctx, *ledger)
	ensureLedger(ctx, *other)
	membership := orKeys(productKeys...)
	fmt.Printf("ledger %s, flow window (0,%d] fixed before any write, %s membership first, K=%d\n", *ledger, T, "Or(payment_ref, invoice_no, refund_no)", *ranges)

	read := func() (int, time.Duration, time.Duration) {
		var slowest atomic.Int64
		span := (T + uint64(*ranges) - 1) / uint64(*ranges)
		var n atomic.Int64
		var wg sync.WaitGroup
		t0 := time.Now()
		for r := 0; r < *ranges; r++ {
			lo := uint64(r) * span
			hi := min(lo+span, T)
			wg.Add(1)
			go func(lo, hi uint64) {
				defer wg.Done()
				filter := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: []*commonpb.QueryFilter{membership,
					{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID,
						Cond: &commonpb.UintCondition{Min: &lo, MinExclusive: true, Max: &hi}}}}}}}}
				var cursor string
				for {
					p0 := time.Now()
					stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor, Reverse: true, Filter: filter}})
					if err != nil {
						log.Fatalf("list transactions: %v", err)
					}
					for {
						_, rerr := stream.Recv()
						if errors.Is(rerr, io.EOF) {
							break
						}
						if rerr != nil {
							log.Fatalf("recv: %v", rerr)
						}
						n.Add(1)
					}
					if d := int64(time.Since(p0)); d > slowest.Load() {
						slowest.Store(d)
					}
					cursor = ""
					if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
						cursor = vals[0]
					}
					if cursor == "" {
						return
					}
				}
			}(lo, hi)
		}
		wg.Wait()
		return int(n.Load()), time.Since(t0), time.Duration(slowest.Load())
	}
	// write runs the writers against target until stop closes, and returns
	// the transactions they wrote.
	write := func(target string, stop chan struct{}, done *atomic.Uint64, wg *sync.WaitGroup) {
		for w := 0; w < *writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for k := 0; ; k++ {
					select {
					case <-stop:
						return
					default:
					}
					var reqs []*servicepb.Request
					for i := 0; i < *batch; i++ {
						id := fmt.Sprintf("W%d-%d-%d", w, k, i)
						reqs = append(reqs, createTx(target, map[string]string{"payment_ref": "P-" + id, "invoice_no": "INV-" + id},
							usd("main:clearing:stripe", "main:hold:invoice:INV-"+id, 100)))
					}
					if _, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}}); err == nil {
						done.Add(uint64(len(reqs)))
					}
				}
			}(w)
		}
	}
	var want int
	for _, c := range []struct{ name, target string }{{"idle", ""}, {"writes on the same ledger", *ledger}, {"writes on another ledger", *other}} {
		var stop chan struct{}
		var done atomic.Uint64
		var wg sync.WaitGroup
		t0 := time.Now()
		if c.target != "" {
			stop = make(chan struct{})
			write(c.target, stop, &done, &wg)
			time.Sleep(3 * time.Second) // let the writers and the indexer reach their pace
		}
		var times []time.Duration
		var worstPage time.Duration
		n := 0
		for r := 0; r < *reps; r++ {
			rows, el, slow := read()
			n = rows
			times = append(times, el)
			worstPage = max(worstPage, slow)
		}
		rate := 0.0
		if stop != nil {
			close(stop)
			wg.Wait()
			rate = float64(done.Load()) / time.Since(t0).Seconds()
		}
		if c.target == "" {
			want = n
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		fmt.Printf("  %-28s rows %d (same: %v)  read %s median, %s best  slowest page %s  writers %.0f tx/s\n",
			c.name, n, n == want, times[len(times)/2].Round(time.Millisecond), times[0].Round(time.Millisecond), worstPage.Round(time.Millisecond), rate)
	}
}

// lookups measures the flow's lookups by key (EN-2318): one ListTransactions
// per reference on `payment_ref = ref` and `id ≤ T`, K at a time, against the
// same references grouped into an Or of equalities (the ledger has no IN), per
// batch size and in both filter orders.
func lookups(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("lookups", flag.ExitOnError)
	ledger := fs.String("ledger", "product-n9", "")
	n := fs.Int("n", 10000, "references looked up")
	k := fs.Int("k", 8, "concurrent lookups")
	batchesArg := fs.String("batches", "1,10,100,500", "references per query")
	_ = fs.Parse(args)
	_, T := ledgerStats(ctx, *ledger)
	// P-i exists for i % 10 != 0 (load-product), one application each.
	var refs []string
	for i := 0; i < 50000 && len(refs) < *n; i++ {
		// 7919 is coprime with 50,000, so j runs over every invoice once, out of order.
		if j := i*7919%50000 + 1; j%10 != 0 {
			refs = append(refs, fmt.Sprintf("P-%d", j))
		}
	}
	eq := func(ref string) *commonpb.QueryFilter {
		return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Field{Field: &commonpb.FieldCondition{
			Field:     &commonpb.FieldRef{Metadata: "payment_ref"},
			Condition: &commonpb.FieldCondition_StringCond{StringCond: &commonpb.StringCondition{Value: &commonpb.StringCondition_Hardcoded{Hardcoded: ref}}},
		}}}
	}
	zero := uint64(0)
	upTo := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
		Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID, Cond: &commonpb.UintCondition{Min: &zero, MinExclusive: true, Max: &T}}}}
	fmt.Printf("ledger %s: %d lookups by payment_ref, id ≤ %d, %d at a time\n", *ledger, *n, T, *k)
	for _, bs := range strings.Split(*batchesArg, ",") {
		var size int
		fmt.Sscan(bs, &size)
		for _, order := range []string{"key first", "id first"} {
			if size == 1 && order == "id first" {
				continue
			}
			jobs := make(chan []string)
			var rows atomic.Int64
			var wg sync.WaitGroup
			t0 := time.Now()
			for w := 0; w < *k; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for group := range jobs {
						var terms []*commonpb.QueryFilter
						for _, r := range group {
							terms = append(terms, eq(r))
						}
						m := terms[0]
						if len(terms) > 1 {
							m = &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Or{Or: &commonpb.OrFilter{Filters: terms}}}
						}
						parts := []*commonpb.QueryFilter{m, upTo}
						if order == "id first" {
							parts = []*commonpb.QueryFilter{upTo, m}
						}
						filter := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: parts}}}
						var cursor string
						for {
							stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor, Reverse: true, Filter: filter}})
							if err != nil {
								log.Fatalf("lookup: %v", err)
							}
							for {
								_, rerr := stream.Recv()
								if errors.Is(rerr, io.EOF) {
									break
								}
								if rerr != nil {
									log.Fatalf("recv: %v", rerr)
								}
								rows.Add(1)
							}
							cursor = ""
							if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
								cursor = vals[0]
							}
							if cursor == "" {
								break
							}
						}
					}
				}()
			}
			for i := 0; i < len(refs); i += size {
				jobs <- refs[i:min(i+size, len(refs))]
			}
			close(jobs)
			wg.Wait()
			el := time.Since(t0)
			label := fmt.Sprintf("%d per query", size)
			if size > 1 {
				label += ", " + order
			}
			fmt.Printf("  %-26s %6d rows  %8s  %6.0f lookups/s  %6.2f ms per lookup\n", label, rows.Load(), el.Round(time.Millisecond), float64(len(refs))/el.Seconds(), 1000*el.Seconds()/float64(len(refs)))
		}
	}
}
