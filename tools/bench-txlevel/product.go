package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Product-ledger keys: the payment reference on applications, and one business
// id per hold kind on every transaction that touches a hold of that kind
// (ADR-005 §5, §8.3).
var productKeys = []string{"payment_ref", "invoice_no", "refund_no"}

// loadProduct books a product ledger the way ADR-005 §8 asks, per invoice i:
//
//   - opening: main:hold:invoice:INV-i → user:revenue:{acct}:pending, with invoice_no;
//   - for 9 invoices in 10, the application as one batch: main:clearing:stripe →
//     the hold, with payment_ref and invoice_no, then the revenue recognition
//     …:pending → user:revenue:{acct}, with no key;
//   - for 1 invoice in 25, a refund: its hold opened with refund_no, then applied
//     with payment_ref and refund_no;
//   - -noise unrelated transactions with no key (orders, internal transfers).
//
// Holds are EPHEMERAL, and every key is declared and indexed.
func loadProduct(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("load-product", flag.ExitOnError)
	ledger := fs.String("ledger", "product", "")
	n := fs.Uint64("n", 50_000, "invoices")
	noise := fs.Uint64("noise", 1, "unkeyed transactions per invoice")
	batch := fs.Int("batch", 200, "invoices per Apply batch")
	workers := fs.Int("workers", 16, "")
	_ = fs.Parse(args)

	var schema []*commonpb.SetMetadataFieldTypeCommand
	for _, k := range productKeys {
		schema = append(schema, &commonpb.SetMetadataFieldTypeCommand{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: k, Type: commonpb.MetadataType_METADATA_TYPE_STRING})
	}
	eph := commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL
	_, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
		Name: *ledger, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
		AccountTypes: map[string]*commonpb.AccountType{
			"invoice": {Name: "invoice", Pattern: "main:hold:invoice:{id}", Persistence: eph},
			"refund":  {Name: "refund", Pattern: "main:hold:refund:{id}", Persistence: eph},
		},
		InitialSchema: schema,
	}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create ledger: %v", err)
	}
	for _, k := range productKeys {
		_, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{Ledger: *ledger,
			Id: &commonpb.IndexID{Kind: &commonpb.IndexID_Metadata{Metadata: &commonpb.MetadataIndexID{Target: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: k}}}}}})
		if err != nil && status.Code(err) != codes.AlreadyExists {
			log.Fatalf("create index %s: %v", k, err)
		}
	}

	jobs := make(chan []uint64, *workers*2)
	var done atomic.Uint64
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ids := range jobs {
				var reqs []*servicepb.Request
				for _, i := range ids {
					inv, amt := fmt.Sprintf("INV-%d", i), amount(i)
					rev := fmt.Sprintf("user:revenue:%d", i%1000)
					hold := "main:hold:invoice:" + inv
					reqs = append(reqs, createTx(*ledger, map[string]string{"invoice_no": inv}, usd(hold, rev+":pending", amt)))
					if i%10 != 0 {
						reqs = append(reqs,
							createTx(*ledger, map[string]string{"payment_ref": fmt.Sprintf("P-%d", i), "invoice_no": inv}, usd("main:clearing:stripe", hold, amt)),
							createTx(*ledger, nil, usd(rev+":pending", rev, amt)))
					}
					if i%25 == 0 {
						rf := fmt.Sprintf("RF-%d", i)
						rhold := "main:hold:refund:" + rf
						reqs = append(reqs,
							createTx(*ledger, map[string]string{"refund_no": rf}, usd(rev, rhold, amt/2)),
							createTx(*ledger, map[string]string{"payment_ref": fmt.Sprintf("R-%d", i), "refund_no": rf}, usd(rhold, "main:clearing:stripe", amt/2)))
					}
					for k := uint64(0); k < *noise; k++ {
						reqs = append(reqs, createTx(*ledger, nil, usd("world", fmt.Sprintf("internal:%d", (i+k)%1000), 1)))
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
				done.Add(uint64(len(reqs)))
			}
		}()
	}
	cur := make([]uint64, 0, *batch)
	for i := uint64(1); i <= *n; i++ {
		cur = append(cur, i)
		if len(cur) >= *batch {
			jobs <- cur
			cur = make([]uint64, 0, *batch)
		}
	}
	if len(cur) > 0 {
		jobs <- cur
	}
	close(jobs)
	wg.Wait()
	el := time.Since(t0)
	fmt.Printf("loaded %d invoices, %d tx (%d unkeyed noise per invoice) on %s in %s (%.0f tx/s)\n",
		*n, done.Load(), *noise, *ledger, el.Round(time.Millisecond), float64(done.Load())/el.Seconds())
}

// idsWindow returns the ids of the transactions in (lo, hi] that match
// membership (nil = every transaction), read over k id ranges in id order.
//
// The ledger keeps an And's children in the order given (query/compile.go
// compileAnd) and drives it from the first. shape says how the membership meets
// the id range: "id-first" And(id, m), "member-first" And(m, id), or
// "or-of-ands" Or(And(key, id)…) when m is an Or of key terms.
func idsWindow(ctx context.Context, ledger string, lo, hi uint64, k int, membership *commonpb.QueryFilter, shape string) ([]uint64, time.Duration) {
	span := (hi - lo + uint64(k) - 1) / uint64(k)
	parts := make([][]uint64, k)
	var wg sync.WaitGroup
	t0 := time.Now()
	for r := 0; r < k; r++ {
		rlo := lo + uint64(r)*span
		rhi := min(rlo+span, hi)
		if rlo >= rhi {
			continue
		}
		wg.Add(1)
		go func(r int, rlo, rhi uint64) {
			defer wg.Done()
			filter := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
				Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID, Cond: &commonpb.UintCondition{Min: &rlo, MinExclusive: true, Max: &rhi},
			}}}
			and := func(fs ...*commonpb.QueryFilter) *commonpb.QueryFilter {
				return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: fs}}}
			}
			switch {
			case membership == nil:
			case shape == "member-first":
				filter = and(membership, filter)
			case shape == "or-of-ands" && membership.GetOr() != nil:
				var terms []*commonpb.QueryFilter
				for _, t := range membership.GetOr().GetFilters() {
					terms = append(terms, and(t, filter))
				}
				filter = &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Or{Or: &commonpb.OrFilter{Filters: terms}}}
			default:
				filter = and(filter, membership)
			}
			var cursor string
			for {
				stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor, Reverse: true, Filter: filter}})
				if err != nil {
					log.Fatalf("list transactions: %v", err)
				}
				retry := false
				for {
					tx, rerr := stream.Recv()
					if errors.Is(rerr, io.EOF) {
						break
					}
					if status.Code(rerr) == codes.Unavailable && cursor == "" && len(parts[r]) == 0 {
						retry = true // index still building
						break
					}
					if rerr != nil {
						log.Fatalf("recv: %v", rerr)
					}
					parts[r] = append(parts[r], tx.GetId())
				}
				if retry {
					time.Sleep(500 * time.Millisecond)
					continue
				}
				cursor = ""
				if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
					cursor = vals[0]
				}
				if cursor == "" {
					return
				}
			}
		}(r, rlo, rhi)
	}
	wg.Wait()
	el := time.Since(t0)
	var all []uint64
	for _, p := range parts {
		all = append(all, p...)
	}
	return all, el
}

func existsKey(key string) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Field{Field: &commonpb.FieldCondition{
		Field: &commonpb.FieldRef{Metadata: key}, Condition: &commonpb.FieldCondition_ExistsCond{ExistsCond: &commonpb.ExistsCondition{}},
	}}}
}

func orKeys(keys ...string) *commonpb.QueryFilter {
	if len(keys) == 1 {
		return existsKey(keys[0])
	}
	var fs []*commonpb.QueryFilter
	for _, k := range keys {
		fs = append(fs, existsKey(k))
	}
	return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Or{Or: &commonpb.OrFilter{Filters: fs}}}
}

// productOr times the product-side flow read (ADR-005 §5): the membership
// Or(payment_ref EXISTS, one business-id EXISTS per hold kind), against the
// PSP-style payment_ref EXISTS alone, the unfiltered window, and the same union
// computed client-side from one read per key. It checks that the Or returns
// exactly the union of its terms. Each figure is the best of -reps runs.
func productOr(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("product-or", flag.ExitOnError)
	ledger := fs.String("ledger", "product", "")
	ranges := fs.Int("ranges", 8, "")
	reps := fs.Int("reps", 3, "")
	from := fs.Uint64("from", 0, "window start (exclusive); the window ends at the head")
	_ = fs.Parse(args)
	_, head := ledgerStats(ctx, *ledger)
	fmt.Printf("ledger %s, window (%d,%d], K=%d, reverse=true, best of %d\n", *ledger, *from, head, *ranges, *reps)

	shape := "id-first"
	best := func(membership *commonpb.QueryFilter) ([]uint64, time.Duration) {
		var ids []uint64
		var b time.Duration
		for r := 0; r < *reps; r++ {
			got, el := idsWindow(ctx, *ledger, *from, head, *ranges, membership, shape)
			if b == 0 || el < b {
				b = el
			}
			ids = got
		}
		return ids, b
	}
	show := func(name string, n int, el time.Duration, base time.Duration) {
		rel := ""
		if base > 0 {
			rel = fmt.Sprintf("  %+.0f %%", 100*(el.Seconds()/base.Seconds()-1))
		}
		fmt.Printf("  %-46s %8d rows  %8s  %7.0f rows/s%s\n", name, n, el.Round(time.Millisecond), float64(n)/el.Seconds(), rel)
	}

	all, tAll := best(nil)
	show("unfiltered", len(all), tAll, 0)
	sets := map[string][]uint64{}
	var tKeys, tPay time.Duration
	for _, k := range productKeys {
		ids, el := best(existsKey(k))
		sets[k] = ids
		tKeys += el
		if k == "payment_ref" {
			tPay = el
		}
		show(k+" EXISTS", len(ids), el, 0)
	}
	pay := sets["payment_ref"]
	union := map[uint64]bool{}
	entries := 0
	for _, k := range productKeys {
		entries += len(sets[k])
		for _, id := range sets[k] {
			union[id] = true
		}
	}
	fmt.Println()
	two, tTwo := best(orKeys("payment_ref", "invoice_no"))
	show("Or(payment_ref, invoice_no)", len(two), tTwo, tPay)
	three, tThree := best(orKeys(productKeys...))
	show("Or(payment_ref, invoice_no, refund_no)", len(three), tThree, tPay)
	show("the three EXISTS reads, merged client-side", len(union), tKeys, tPay)

	isUnion := func(ids []uint64) bool {
		ids = append([]uint64(nil), ids...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) != len(union) {
			return false
		}
		for i, id := range ids {
			if !union[id] || (i > 0 && ids[i-1] == id) {
				return false
			}
		}
		return true
	}
	exact := isUnion(three)

	// The same reads with the membership driving the And, or with the id range
	// pushed into each term of the Or.
	fmt.Println("\nmembership first, And(membership, id):")
	shape = "member-first"
	payF, tPayF := best(existsKey("payment_ref"))
	show("payment_ref EXISTS", len(payF), tPayF, tPay)
	twoF, tTwoF := best(orKeys("payment_ref", "invoice_no"))
	show("Or(payment_ref, invoice_no)", len(twoF), tTwoF, tPay)
	threeF, tThreeF := best(orKeys(productKeys...))
	show("Or(payment_ref, invoice_no, refund_no)", len(threeF), tThreeF, tPay)
	fmt.Println("\nOr(And(key, id), …), one term per key:")
	shape = "or-of-ands"
	threeA, tThreeA := best(orKeys(productKeys...))
	show("Or(And(payment_ref, id), …, And(refund_no, id))", len(threeA), tThreeA, tPay)
	exact = exact && isUnion(threeF) && isUnion(threeA) && len(payF) == len(pay) && len(twoF) == len(two)
	fmt.Printf("\nevery Or read = the union of its terms, with no duplicate: %v\n", exact)
	fmt.Printf("index entries read by the Or: %d for %d rows returned (%.2f per row); payment_ref alone: %d rows\n",
		entries, len(union), float64(entries)/float64(len(union)), len(pay))
	fmt.Printf("share of the window in the flow: %.0f %%\n", 100*float64(len(union))/float64(len(all)))
}
