package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	dayPSP        = "pspday"
	dayProduct    = "prodday"
	dayPSPHold    = "fpay:stripe:payment:hold:pending:"
	dayPayAccount = "fpay:stripe:account:acct_1:main"
	dayInvoice    = "main:hold:invoice:"
)

// dayState is what day-load leaves for day-run: per ledger, the heads at the end of
// each day, and the insertion date of the day's last transaction (its cut-off).
type dayState struct {
	Ledgers map[string]dayHeads `json:"ledgers"`
}

type dayHeads struct {
	PrevLog, PrevTx, PrevCutoff uint64
	DayLog, DayTx, DayCutoff    uint64
}

func createDayLedger(ctx context.Context, name, holdType, holdPattern string, keys []string) {
	var schema []*commonpb.SetMetadataFieldTypeCommand
	for _, k := range keys {
		schema = append(schema, &commonpb.SetMetadataFieldTypeCommand{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: k, Type: commonpb.MetadataType_METADATA_TYPE_STRING})
	}
	_, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
		Name: name, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
		AccountTypes:  map[string]*commonpb.AccountType{holdType: {Name: holdType, Pattern: holdPattern, Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL}},
		InitialSchema: schema,
	}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create ledger %s: %v", name, err)
	}
	ids := []*commonpb.IndexID{
		{Kind: &commonpb.IndexID_LogBuiltin{LogBuiltin: commonpb.LogBuiltinIndex_LOG_BUILTIN_INDEX_DATE}},
		{Kind: &commonpb.IndexID_TxBuiltin{TxBuiltin: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_INSERTED_AT}},
	}
	for _, k := range keys {
		ids = append(ids, &commonpb.IndexID{Kind: &commonpb.IndexID_Metadata{Metadata: &commonpb.MetadataIndexID{Target: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: k}}})
	}
	for _, id := range ids {
		if _, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{Ledger: name, Id: id}}}); err != nil && status.Code(err) != codes.AlreadyExists {
			log.Fatalf("create index on %s: %v", name, err)
		}
	}
}

// bookItems books items [from, from+n) through workers, each item giving its requests.
func bookItems(ctx context.Context, from, n, batch, workers int, item func(i int) []*servicepb.Request) {
	jobs := make(chan [2]int, workers*2)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				var reqs []*servicepb.Request
				for i := r[0]; i < r[1]; i++ {
					reqs = append(reqs, item(i)...)
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
	for i := from; i < from+n; i += batch {
		jobs <- [2]int{i, min(i+batch, from+n)}
	}
	close(jobs)
	wg.Wait()
}

// pspItem books one PSP payment: pending (world -> its hold), then, for 97 in 100, final
// (hold -> payment account), both keyed; the other 3 stay pending, so their hold stays open.
func dayPSPItem(i int) []*servicepb.Request {
	ref := fmt.Sprintf("pi_%024d", i)
	amt := amount(uint64(i))
	hold := dayPSPHold + ref
	reqs := []*servicepb.Request{createTx(dayPSP, map[string]string{"payment_ref": ref, "state": "payin.pending"}, usd("world", hold, amt))}
	if i%100 >= 3 {
		reqs = append(reqs, createTx(dayPSP, map[string]string{"payment_ref": ref, "state": "payin.succeeded"}, usd(hold, dayPayAccount, amt)))
	}
	return reqs
}

// dayProductItem books one invoice: opened with invoice_no; for 9 in 10, applied with
// payment_ref and invoice_no plus an unkeyed revenue recognition; one unkeyed transaction
// besides.
func dayProductItem(i int) []*servicepb.Request {
	inv := fmt.Sprintf("INV-%09d", i)
	amt := amount(uint64(i))
	rev := fmt.Sprintf("user:revenue:%d", i%1000)
	hold := dayInvoice + inv
	reqs := []*servicepb.Request{createTx(dayProduct, map[string]string{"invoice_no": inv}, usd(hold, rev+":pending", amt))}
	if i%10 != 0 {
		reqs = append(reqs,
			createTx(dayProduct, map[string]string{"payment_ref": fmt.Sprintf("pi_%024d", i), "invoice_no": inv}, usd("main:clearing:stripe", hold, amt)),
			createTx(dayProduct, nil, usd(rev+":pending", rev, amt)))
	}
	return append(reqs, createTx(dayProduct, nil, usd("world", fmt.Sprintf("internal:%d", i%1000), 1)))
}

func dayLoad(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("day-load", flag.ExitOnError)
	prev := fs.Int("prev", 300_000, "payments of the day before")
	day := fs.Int("day", 1_000_000, "payments of the day")
	stateFile := fs.String("state", "day-state.json", "")
	_ = fs.Parse(args)
	createDayLedger(ctx, dayPSP, "hold", dayPSPHold+"{id}", []string{"payment_ref", "state"})
	createDayLedger(ctx, dayProduct, "invoice", dayInvoice+"{id}", []string{"payment_ref", "invoice_no"})
	st := dayState{Ledgers: map[string]dayHeads{}}
	heads := func(ledger string) (uint64, uint64, uint64) {
		l, t := ledgerStats(ctx, ledger)
		stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1}})
		if err != nil {
			log.Fatal(err)
		}
		tx, err := stream.Recv()
		if err != nil {
			log.Fatal(err)
		}
		return l, t, tx.GetInsertedAt().GetData() // newest first by default: the last transaction
	}
	for _, phase := range []struct {
		name     string
		from, n  int
		dayPhase bool
	}{{"day before", 0, *prev, false}, {"day", *prev, *day, true}} {
		t0 := time.Now()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); bookItems(ctx, phase.from, phase.n, 250, 8, dayPSPItem) }()
		go func() { defer wg.Done(); bookItems(ctx, phase.from, phase.n, 100, 8, dayProductItem) }()
		wg.Wait()
		time.Sleep(1100 * time.Millisecond) // the next day's first transaction gets a later date
		for _, l := range []string{dayPSP, dayProduct} {
			h := st.Ledgers[l]
			lg, tx, date := heads(l)
			if phase.dayPhase {
				h.DayLog, h.DayTx, h.DayCutoff = lg, tx, date
			} else {
				h.PrevLog, h.PrevTx, h.PrevCutoff = lg, tx, date
			}
			st.Ledgers[l] = h
		}
		fmt.Printf("%s: %d payments on %s and %s in %s\n", phase.name, phase.n, dayPSP, dayProduct, time.Since(t0).Round(time.Second))
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(*stateFile, b, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(b))
}

// readerCap is the process-wide cap on concurrent readers (--lettering-max-concurrent-reads):
// a step takes all its slots at once, so two steps never deadlock on half their slots.
type readerCap struct {
	mu    sync.Mutex
	slots chan struct{}
	wait  atomic.Int64
}

func (c *readerCap) acquire(n int) (func(), time.Duration) {
	t0 := time.Now()
	c.mu.Lock()
	for i := 0; i < n; i++ {
		c.slots <- struct{}{}
	}
	c.mu.Unlock()
	w := time.Since(t0)
	c.wait.Add(int64(w))
	return func() {
		for i := 0; i < n; i++ {
			<-c.slots
		}
	}, w
}

// dayRun runs one day's lettering reads on both ledgers, as the design doc lays out a run
// (§3–§5), while writers keep booking: per side, the cut, then the flow read and its grouped
// lookups, the live listing and its rewind, the metadata watch and the payment-account read,
// all through one cap of -cap readers. It reports each step and the run's wall time.
func dayRun(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("day-run", flag.ExitOnError)
	stateFile := fs.String("state", "day-state.json", "")
	k := fs.Int("ranges", 8, "")
	capN := fs.Int("cap", 16, "")
	trickle := fs.Int("trickle", 50, "transactions/s per ledger written while the run reads (0: none)")
	afterCut := fs.Int("after-cut", 0, "payments booked on each ledger just before the run, after the cut (the backlog a run 2 h after the cut-off finds)")
	lookupsN := fs.Int("lookups", 1000, "references looked up on the PSP side, grouped by 100")
	_ = fs.Parse(args)
	b, err := os.ReadFile(*stateFile)
	if err != nil {
		log.Fatal(err)
	}
	var st dayState
	if err := json.Unmarshal(b, &st); err != nil {
		log.Fatal(err)
	}

	if *afterCut > 0 {
		t0 := time.Now()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); bookItems(ctx, 20_000_000, *afterCut, 250, 8, dayPSPItem) }()
		go func() { defer wg.Done(); bookItems(ctx, 20_000_000, *afterCut, 100, 8, dayProductItem) }()
		wg.Wait()
		fmt.Printf("after the cut: %d payments booked on each ledger in %s\n", *afterCut, time.Since(t0).Round(time.Second))
	}
	stop := make(chan struct{})
	var written atomic.Int64
	var wwg sync.WaitGroup
	if *trickle > 0 {
		for _, l := range []string{dayPSP, dayProduct} {
			wwg.Add(1)
			go func(l string) {
				defer wwg.Done()
				tick := time.NewTicker(100 * time.Millisecond)
				defer tick.Stop()
				n := 30_000_000 + int(time.Now().UnixNano()%1_000_000)*10
				for {
					select {
					case <-stop:
						return
					case <-tick.C:
					}
					var reqs []*servicepb.Request
					for len(reqs) < *trickle/10 {
						if l == dayPSP {
							reqs = append(reqs, dayPSPItem(n)...)
						} else {
							reqs = append(reqs, dayProductItem(n)...)
						}
						n++
					}
					if _, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}}); err == nil {
						written.Add(int64(len(reqs)))
					}
				}
			}(l)
		}
	}
	rc := &readerCap{slots: make(chan struct{}, *capN)}
	type result struct {
		side, step string
		rows       int
		took, wait time.Duration
	}
	var mu sync.Mutex
	var results []result
	record := func(side, step string, rows int, took, wait time.Duration) {
		mu.Lock()
		results = append(results, result{side, step, rows, took, wait})
		mu.Unlock()
	}
	t0 := time.Now()
	var wg sync.WaitGroup
	for _, side := range []string{dayPSP, dayProduct} {
		wg.Add(1)
		go func(ledger string) {
			defer wg.Done()
			h := st.Ledgers[ledger]
			// The cut: S and T of the day, and of the day before, bounded and widened while empty.
			release, cw := rc.acquire(1)
			c0 := time.Now()
			resolve := func(cutoff uint64) (uint64, uint64) {
				s, err := widening(func(d uint64) (uint64, error) {
					id, _, err := firstLogAfter(ctx, ledger, cutoff, cutoff+d)
					return id, err
				}, 1_000_000)
				if err != nil {
					log.Fatalf("cut S: %v", err)
				}
				t, err := widening(func(d uint64) (uint64, error) {
					id, _, err := firstTxAfter(ctx, ledger, cutoff, cutoff+d)
					return id, err
				}, 1_000_000)
				if err != nil {
					log.Fatalf("cut T: %v", err)
				}
				return s - 1, t - 1
			}
			sPrev, tPrev := resolve(h.PrevCutoff)
			S, T := resolve(h.DayCutoff)
			release()
			if S != h.DayLog || T != h.DayTx || sPrev != h.PrevLog || tPrev != h.PrevTx {
				log.Fatalf("%s: cut S=%d T=%d prev %d %d, loaded %d %d prev %d %d", ledger, S, T, sPrev, tPrev, h.DayLog, h.DayTx, h.PrevLog, h.PrevTx)
			}
			record(ledger, "cut (S, T and the previous day's)", 4, time.Since(c0), cw)
			headLog, _ := ledgerStats(ctx, ledger)

			var sw sync.WaitGroup
			sw.Add(4)
			go func() { // the flow read, then its lookups
				defer sw.Done()
				membership := existsKey("payment_ref")
				if ledger == dayProduct {
					membership = orKeys("payment_ref", "invoice_no")
				}
				rel, fw := rc.acquire(*k)
				f0 := time.Now()
				ids, _ := idsWindow(ctx, ledger, tPrev, T, *k, membership, "member-first")
				rel()
				record(ledger, "flow, membership first", len(ids), time.Since(f0), fw)
				if ledger != dayPSP {
					return
				}
				rel, lw := rc.acquire(1)
				l0 := time.Now()
				rows := 0
				for g := 0; g < *lookupsN; g += 100 {
					var terms []*commonpb.QueryFilter
					for i := g; i < min(g+100, *lookupsN); i++ {
						terms = append(terms, &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Field{Field: &commonpb.FieldCondition{
							Field:     &commonpb.FieldRef{Metadata: "payment_ref"},
							Condition: &commonpb.FieldCondition_StringCond{StringCond: &commonpb.StringCondition{Value: &commonpb.StringCondition_Hardcoded{Hardcoded: fmt.Sprintf("pi_%024d", i*37)}}},
						}}})
					}
					got, _ := idsWindow(ctx, ledger, 0, T, 1, &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Or{Or: &commonpb.OrFilter{Filters: terms}}}, "member-first")
					rows += len(got)
				}
				rel()
				record(ledger, fmt.Sprintf("lookups, %d refs by 100", *lookupsN), rows, time.Since(l0), lw)
			}()
			go func() { // the live listing of the open holds, then the rewind to T
				defer sw.Done()
				prefix := dayPSPHold
				if ledger == dayProduct {
					prefix = dayInvoice
				}
				rel, sw1 := rc.acquire(1)
				s0 := time.Now()
				n, _, _, err := scan(ctx, ledger, prefix, 0, 1000, func(row) {})
				rel()
				if err != nil {
					log.Fatalf("scan: %v", err)
				}
				listed := time.Since(s0)
				_, lt := ledgerStats(ctx, ledger)
				rel, sw2 := rc.acquire(*k)
				r0 := time.Now()
				wr := readWindow(ctx, ledger, "txs", T, lt, *k, prefixMatch(prefix))
				rel()
				record(ledger, fmt.Sprintf("stock: listing %d holds (%s) + rewind", n, listed.Round(time.Millisecond)), int(wr.items), listed+time.Since(r0), sw1+sw2)
			}()
			go func() { // the metadata watch: every log since the previous run's head
				defer sw.Done()
				rel, ww := rc.acquire(*k)
				w0 := time.Now()
				counts, _ := readLogs(ctx, ledger, sPrev, headLog, *k)
				rel()
				var n uint64
				for _, v := range counts {
					n += v
				}
				record(ledger, "metadata watch", int(n), time.Since(w0), ww)
			}()
			go func() { // the payment-account book
				defer sw.Done()
				if ledger != dayPSP {
					return
				}
				rel, pw := rc.acquire(1)
				p0 := time.Now()
				_ = accountPair(ctx, ledger, dayPayAccount)
				rel()
				record(ledger, "payment-account read", 1, time.Since(p0), pw)
			}()
			sw.Wait()
		}(side)
	}
	wg.Wait()
	total := time.Since(t0)
	close(stop)
	wwg.Wait()
	sort.Slice(results, func(i, j int) bool {
		if results[i].side != results[j].side {
			return results[i].side < results[j].side
		}
		return results[i].took > results[j].took
	})
	for _, r := range results {
		fmt.Printf("  %-8s %-48s %9d rows  read %9s  waited %9s\n", r.side, r.step, r.rows, r.took.Round(time.Millisecond), r.wait.Round(time.Millisecond))
	}
	fmt.Printf("run: %s wall clock, K=%d, cap %d, time waiting for slots %s; writers booked %d transactions during it (%.0f tx/s)\n",
		total.Round(time.Millisecond), *k, *capN, time.Duration(rc.wait.Load()).Round(time.Millisecond), written.Load(), float64(written.Load())/total.Seconds())
}
