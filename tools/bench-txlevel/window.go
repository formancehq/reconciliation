package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pair is an account's USD/2 volumes. Balances are input − output.
type pair struct{ in, out *big.Int }

func newPair() pair              { return pair{new(big.Int), new(big.Int)} }
func (p pair) balance() *big.Int { return new(big.Int).Sub(p.in, p.out) }

// windowRead is what a window read of the balance-moving transactions returns:
// per account under the prefix (or in the exact set), its volumes just before
// its first touch in the window (first) and just after its last touch (last).
type windowRead struct {
	first, last map[string]pair
	items       uint64 // rows streamed: logs or transactions
	txs         uint64 // rows that carried a created or revert transaction
	reverts     uint64
	elapsed     time.Duration
}

// match says whether an address belongs to the scope the window folds.
type match func(addr string) bool

func prefixMatch(prefix string) match {
	return func(a string) bool { return strings.HasPrefix(a, prefix) }
}

// ownNet returns, per account in scope, the transaction's own USD/2 credits and
// debits on it.
func ownNet(tx *commonpb.Transaction, in match) map[string]pair {
	net := map[string]pair{}
	get := func(a string) pair {
		p, ok := net[a]
		if !ok {
			p = newPair()
			net[a] = p
		}
		return p
	}
	for _, p := range tx.GetPostings() {
		if p.GetAsset() != "USD/2" {
			continue
		}
		amt := p.GetAmount().ToBigInt()
		if in(p.GetDestination()) {
			get(p.GetDestination()).in.Add(get(p.GetDestination()).in, amt)
		}
		if in(p.GetSource()) {
			get(p.GetSource()).out.Add(get(p.GetSource()).out, amt)
		}
	}
	return net
}

func pcvPair(vba *commonpb.VolumesByAssets) pair {
	p := newPair()
	for _, v := range vba.GetVolumes() {
		if v.GetAsset() != "USD/2" {
			continue
		}
		if in, ok := new(big.Int).SetString(v.GetVolumes().GetInput(), 10); ok {
			p.in.Add(p.in, in)
		}
		if out, ok := new(big.Int).SetString(v.GetVolumes().GetOutput(), 10); ok {
			p.out.Add(p.out, out)
		}
	}
	return p
}

// foldTx records one balance-moving transaction into the range's first/last maps.
func foldTx(tx *commonpb.Transaction, in match, first, last map[string]pair) {
	var net map[string]pair
	for addr, vba := range tx.GetPostCommitVolumes().GetVolumesByAccount() {
		if !in(addr) {
			continue
		}
		post := pcvPair(vba)
		last[addr] = post
		if _, seen := first[addr]; seen {
			continue
		}
		if net == nil {
			net = ownNet(tx, in)
		}
		own, ok := net[addr]
		if !ok {
			own = newPair()
		}
		first[addr] = pair{new(big.Int).Sub(post.in, own.in), new(big.Int).Sub(post.out, own.out)}
	}
}

// readWindow reads the ids (lo, hi] from source "logs" (per-ledger log ids,
// ListLogs) or "txs" (transaction ids, unfiltered ListTransactions), split into
// k disjoint ranges read concurrently, and folds every balance-moving
// transaction on the accounts in scope. Ranges are merged in id order: the
// lowest range that touched an account gives its first touch, the highest its
// last.
func readWindow(ctx context.Context, ledger, source string, lo, hi uint64, k int, in match) windowRead {
	if k < 1 {
		k = 1
	}
	type part struct {
		first, last         map[string]pair
		items, txs, reverts uint64
	}
	parts := make([]part, k)
	span := (hi - lo + uint64(k) - 1) / uint64(k)
	var wg sync.WaitGroup
	t0 := time.Now()
	for r := 0; r < k; r++ {
		rlo := lo + uint64(r)*span
		rhi := min(rlo+span, hi)
		parts[r] = part{first: map[string]pair{}, last: map[string]pair{}}
		if rlo >= rhi {
			continue
		}
		wg.Add(1)
		go func(p *part, rlo, rhi uint64) {
			defer wg.Done()
			cond := &commonpb.UintCondition{Min: &rlo, MinExclusive: true, Max: &rhi}
			var cursor string
			for {
				var next string
				switch source {
				case "logs":
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
						p.items++
						data := l.GetPayload().GetApply().GetLog().GetData()
						tx := data.GetCreatedTransaction().GetTransaction()
						if tx == nil {
							tx = data.GetRevertedTransaction().GetRevertTransaction()
							if tx != nil {
								p.reverts++
							}
						}
						if tx == nil {
							continue
						}
						p.txs++
						foldTx(tx, in, p.first, p.last)
					}
					if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
						next = vals[0]
					}
				case "txs":
					// The API lists transactions newest first by default;
					// reverse=true gives id order (ledger controller_default.go:375).
					stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor, Reverse: true,
						Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
							Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID, Cond: cond,
						}}}}})
					if err != nil {
						log.Fatalf("list transactions: %v", err)
					}
					for {
						tx, rerr := stream.Recv()
						if errors.Is(rerr, io.EOF) {
							break
						}
						if rerr != nil {
							log.Fatalf("recv transaction: %v", rerr)
						}
						p.items++
						p.txs++
						if tx.GetRevertsTransaction() != 0 {
							p.reverts++
						}
						foldTx(tx, in, p.first, p.last)
					}
					if vals := stream.Trailer().Get("x-next-cursor"); len(vals) > 0 {
						next = vals[0]
					}
				default:
					log.Fatalf("unknown source %q", source)
				}
				if next == "" {
					return
				}
				cursor = next
			}
		}(&parts[r], rlo, rhi)
	}
	wg.Wait()
	res := windowRead{first: map[string]pair{}, last: map[string]pair{}, elapsed: time.Since(t0)}
	for _, p := range parts {
		res.items += p.items
		res.txs += p.txs
		res.reverts += p.reverts
		for a, v := range p.first {
			if _, ok := res.first[a]; !ok {
				res.first[a] = v
			}
		}
		for a, v := range p.last {
			res.last[a] = v
		}
	}
	return res
}

func ledgerStats(ctx context.Context, ledger string) (logs, txs uint64) {
	st, err := svc.GetLedgerStats(ctx, &servicepb.GetLedgerStatsRequest{Ledger: ledger})
	if err != nil {
		log.Fatalf("ledger stats: %v", err)
	}
	return st.GetLogCount(), st.GetTransactionCount()
}

func apply(ctx context.Context, reqs ...*servicepb.Request) (*servicepb.ApplyResponse, error) {
	return svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}})
}

func createTx(ledger string, md map[string]string, postings ...*commonpb.Posting) *servicepb.Request {
	return &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: ledger,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
			Postings: postings, Force: true, Metadata: commonpb.MetadataFromMap(md),
		}}},
	}}}
}

func usd(src, dst string, amt uint64) *commonpb.Posting {
	return &commonpb.Posting{Source: src, Destination: dst, Amount: commonpb.NewUint256FromUint64(amt), Asset: "USD/2"}
}

// rewindSources proves the rewind of a live listing to the cut with two
// window sources side by side: the logs (S, head] as ADR-005 §5 does, and the
// unfiltered transactions (T, head_tx]. Writers run during the listing and mix
// top-ups, drains, new accounts, reverts of transactions older than the cut,
// reverts of transactions written after it, and metadata-only writes. Both
// rewound listings are compared, row for row, with a checkpoint taken at the cut.
func rewindSources(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("rewind-sources", flag.ExitOnError)
	ledger := fs.String("ledger", "psp", "")
	prefix := fs.String("prefix", "psp:tx:", "")
	writers := fs.Int("writers", 8, "")
	ranges := fs.Int("ranges", 8, "concurrent id ranges for the window reads")
	_ = fs.Parse(args)
	in := prefixMatch(*prefix)

	S, T := ledgerStats(ctx, *ledger)
	resp, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateQueryCheckpoint{CreateQueryCheckpoint: &servicepb.CreateQueryCheckpointRequest{}}})
	if err != nil {
		log.Fatalf("checkpoint: %v", err)
	}
	var cp uint64
	for _, l := range resp.GetLogs() {
		if c := l.GetPayload().GetCreatedQueryCheckpoint(); c != nil {
			cp = c.GetCheckpointId()
		}
	}
	if cp == 0 {
		log.Fatal("no checkpoint log in the response")
	}
	if s2, t2 := ledgerStats(ctx, *ledger); s2 != S || t2 != T {
		log.Fatal("a write slipped in between the cut and the checkpoint")
	}
	fmt.Printf("cut S=%d (log id) T=%d (tx id), oracle checkpoint %d\n", S, T, cp)

	ops := []string{"topup", "drain", "new", "revert-old", "revert-new", "metadata"}
	var okByOp, errByOp [6]atomic.Uint64
	stop := make(chan struct{})
	var wwg sync.WaitGroup
	for w := 0; w < *writers; w++ {
		wwg.Add(1)
		go func(w int) {
			defer wwg.Done()
			var k, j, lastNew uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				k++
				i := ((uint64(w)*1_000_000_000 + k) * 2654435761) % 1_000_000
				op := int(k % 6)
				var req *servicepb.Request
				switch op {
				case 0:
					req = createTx(*ledger, nil, usd("world", *prefix+id(i), 7))
				case 1:
					req = createTx(*ledger, nil, usd(*prefix+id(i), "sink", amount(i)))
				case 2:
					req = createTx(*ledger, nil, usd("world", *prefix+id(5_000_000+uint64(w)*1_000_000_000+k), 11))
				case 3:
					// A distinct transaction older than the cut per (writer, j):
					// multiplying by a prime coprime with T is a bijection mod T.
					target := 1 + ((uint64(w)+uint64(*writers)*j)*7919)%T
					j++
					req = &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{Ledger: *ledger,
						Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_RevertTransaction{RevertTransaction: &servicepb.RevertTransactionPayload{TransactionId: target, Force: true}}}}}}
				case 4:
					if lastNew == 0 {
						continue
					}
					req = &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{Ledger: *ledger,
						Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_RevertTransaction{RevertTransaction: &servicepb.RevertTransactionPayload{TransactionId: lastNew, Force: true}}}}}}
					lastNew = 0
				case 5:
					req = &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{Ledger: *ledger,
						Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_AddMetadata{AddMetadata: &commonpb.SaveMetadataCommand{
							Target:   &commonpb.Target{Target: &commonpb.Target_TransactionId{TransactionId: 1 + (i % T)}},
							Metadata: commonpb.MetadataFromMap(map[string]string{"note": fmt.Sprintf("w%d-%d", w, k)}),
						}}}}}}
				}
				r, err := apply(ctx, req)
				if err != nil {
					errByOp[op].Add(1)
					continue
				}
				okByOp[op].Add(1)
				if op == 0 || op == 1 || op == 2 {
					for _, l := range r.GetLogs() {
						if tx := l.GetPayload().GetApply().GetLog().GetData().GetCreatedTransaction().GetTransaction(); tx != nil {
							lastNew = tx.GetId()
						}
					}
				}
			}
		}(w)
	}

	t0 := time.Now()
	listed := map[string]*big.Int{}
	if _, _, _, err := scan(ctx, *ledger, *prefix, 0, 1000, func(r row) { listed[*prefix+r.key] = r.balance }); err != nil {
		log.Fatalf("live scan: %v", err)
	}
	tList := time.Since(t0)
	close(stop)
	wwg.Wait()
	headLog, headTx := ledgerStats(ctx, *ledger)
	var writes []string
	for o := range ops {
		writes = append(writes, fmt.Sprintf("%s %d ok/%d err", ops[o], okByOp[o].Load(), errByOp[o].Load()))
	}
	fmt.Printf("live listing %s, %d rows; writes during it: %s\n", tList.Round(time.Millisecond), len(listed), strings.Join(writes, ", "))
	fmt.Printf("windows: logs (%d,%d] = %d ids, transactions (%d,%d] = %d ids\n", S, headLog, headLog-S, T, headTx, headTx-T)

	oracle := map[string]*big.Int{}
	if _, _, _, err := scan(ctx, *ledger, *prefix, cp, 1000, func(r row) {
		if r.balance.Sign() != 0 {
			oracle[*prefix+r.key] = r.balance
		}
	}); err != nil {
		log.Fatalf("oracle scan: %v", err)
	}
	rawDiff := 0
	for a, v := range oracle {
		if lv, ok := listed[a]; !ok || lv.Cmp(v) != 0 {
			rawDiff++
		}
	}
	for a, v := range listed {
		if _, ok := oracle[a]; !ok && v.Sign() != 0 {
			rawDiff++
		}
	}
	fmt.Printf("oracle (checkpoint at the cut): %d non-zero rows; raw live listing differs on %d rows\n", len(oracle), rawDiff)

	for _, src := range []struct {
		name   string
		lo, hi uint64
	}{{"logs", S, headLog}, {"txs", T, headTx}} {
		for _, k := range []int{1, *ranges} {
			wr := readWindow(ctx, *ledger, src.name, src.lo, src.hi, k, in)
			rebuilt := map[string]*big.Int{}
			for a, v := range listed {
				if _, touched := wr.first[a]; !touched && v.Sign() != 0 {
					rebuilt[a] = v
				}
			}
			for a, v := range wr.first {
				if b := v.balance(); b.Sign() != 0 {
					rebuilt[a] = b
				}
			}
			diffs := 0
			for a, v := range oracle {
				if rv, ok := rebuilt[a]; !ok || rv.Cmp(v) != 0 {
					diffs++
				}
			}
			for a := range rebuilt {
				if _, ok := oracle[a]; !ok {
					diffs++
				}
			}
			complete := wr.items == src.hi-src.lo
			fmt.Printf("rewind from %-4s K=%-2d: %d rows read (count check %v), %d balance-moving tx (%d reverts), %d scope accounts touched, %s (%.0f rows/s) -> differs from the oracle on %d rows\n",
				src.name, k, wr.items, complete, wr.txs, wr.reverts, len(wr.first), wr.elapsed.Round(time.Millisecond), float64(wr.items)/wr.elapsed.Seconds(), diffs)
		}
	}
	cpDelete(ctx, cp)
}

// foldCmd times a window fold over (from, to] from logs or transactions, the
// read a replay or a forward stock pays per day.
func foldCmd(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("fold", flag.ExitOnError)
	ledger := fs.String("ledger", "psp", "")
	prefix := fs.String("prefix", "psp:tx:", "")
	source := fs.String("source", "txs", "logs or txs")
	from := fs.Uint64("from", 0, "")
	to := fs.Uint64("to", 0, "0 = head")
	ranges := fs.Int("ranges", 8, "")
	compare := fs.Bool("compare", false, "compare the forward fold with the live listing (from 0 to the head, no writes)")
	_ = fs.Parse(args)
	if *to == 0 {
		l, t := ledgerStats(ctx, *ledger)
		*to = t
		if *source == "logs" {
			*to = l
		}
	}
	wr := readWindow(ctx, *ledger, *source, *from, *to, *ranges, prefixMatch(*prefix))
	fmt.Printf("fold %s %s (%d,%d] K=%d: %d rows (count check %v), %d tx, %d accounts, %s (%.0f rows/s)\n",
		*ledger, *source, *from, *to, *ranges, wr.items, wr.items == *to-*from, wr.txs, len(wr.last), wr.elapsed.Round(time.Millisecond), float64(wr.items)/wr.elapsed.Seconds())
	if *compare {
		// Forward from an empty stock at 0 up to the head must give the live
		// listing, with no write running.
		listed := map[string]*big.Int{}
		if _, _, _, err := scan(ctx, *ledger, *prefix, 0, 1000, func(r row) { listed[*prefix+r.key] = r.balance }); err != nil {
			log.Fatalf("scan: %v", err)
		}
		diffs := 0
		for a, v := range listed {
			if p, ok := wr.last[a]; !ok || p.balance().Cmp(v) != 0 {
				diffs++
			}
		}
		for a, p := range wr.last {
			if _, ok := listed[a]; !ok && p.balance().Sign() != 0 {
				diffs++
			}
		}
		fmt.Printf("forward fold vs live listing (%d rows): %d rows differ\n", len(listed), diffs)
	}
}

// silentHole builds one day of PSP activity in which some finals have no
// pending before them and no payment reference, and shows what each check
// sees: the flow read and the hold continuity stay green, while the book of the
// PSP payment account (its volumes at S minus at S_prev, against the flow's
// postings on it) exposes them. Writes after the cut exercise the rewind of
// that account, read from the unfiltered transactions.
func silentHole(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("silent", flag.ExitOnError)
	ledger := fs.String("ledger", fmt.Sprintf("silent-%d", time.Now().Unix()), "")
	n := fs.Uint64("n", 100_000, "day-2 payments with a pending then a final")
	open0 := fs.Uint64("open0", 5_000, "pendings opened on day 1")
	settle0 := fs.Uint64("settle0", 3_000, "day-1 pendings finalised on day 2")
	direct := fs.Uint64("direct", 10_000, "keyed finals with no pending (hold untouched)")
	silent := fs.Uint64("silent", 100, "finals with no pending and NO payment reference: the silent hole")
	drain := fs.Uint64("keyless-drain", 10, "control: finals that drain their hold without a reference")
	payouts := fs.Uint64("payouts", 1_000, "payouts debiting the payment account")
	fees := fs.Uint64("fees", 1_000, "fees debiting the payment account")
	refunds := fs.Uint64("refunds", 1_000, "keyed refunds debiting the payment account")
	after := fs.Uint64("after", 5_000, "payments written after the cut (rewind window)")
	keyedOther := fs.Bool("keyed-other", false, "payouts and fees carry a declared movement_ref, read with the flow through an Or")
	ranges := fs.Int("ranges", 8, "")
	workers := fs.Int("workers", 16, "")
	_ = fs.Parse(args)
	const main, holdP, bank, feeAcc = "psp:main", "psp:hold:", "psp:bank:payouts", "psp:fees"

	_, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
		Name: *ledger, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
		AccountTypes: map[string]*commonpb.AccountType{"hold": {Name: "hold", Pattern: holdP + "{id}", Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL}},
		InitialSchema: []*commonpb.SetMetadataFieldTypeCommand{
			{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: "payment_ref", Type: commonpb.MetadataType_METADATA_TYPE_STRING},
			{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: "movement_ref", Type: commonpb.MetadataType_METADATA_TYPE_STRING},
			{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: "state", Type: commonpb.MetadataType_METADATA_TYPE_STRING},
		},
	}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create ledger: %v", err)
	}
	for _, key := range []string{"payment_ref", "movement_ref"} {
		if _, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{Ledger: *ledger,
			Id: &commonpb.IndexID{Kind: &commonpb.IndexID_Metadata{Metadata: &commonpb.MetadataIndexID{Target: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: key}}}}}}); err != nil && status.Code(err) != codes.AlreadyExists {
			log.Fatalf("create index: %v", err)
		}
	}

	ref := func(kind string, i uint64) string { return fmt.Sprintf("%s-%s", kind, id(i)) }
	pay := func(r, state string) map[string]string { return map[string]string{"payment_ref": r, "state": state} }
	other := func(kind string, i uint64) map[string]string {
		if *keyedOther {
			return map[string]string{"movement_ref": ref(kind, i)}
		}
		return nil
	}
	// run applies the lifecycles in batches of 500 requests, 16 at a time.
	// A lifecycle's requests stay in one batch, in order.
	run := func(lifecycles [][]*servicepb.Request) {
		jobs := make(chan []*servicepb.Request, *workers*2)
		var wg sync.WaitGroup
		for w := 0; w < *workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for b := range jobs {
					if _, err := apply(ctx, b...); err != nil {
						log.Fatalf("apply: %v", err)
					}
				}
			}()
		}
		var cur []*servicepb.Request
		for _, lc := range lifecycles {
			cur = append(cur, lc...)
			if len(cur) >= 500 {
				jobs <- cur
				cur = nil
			}
		}
		if len(cur) > 0 {
			jobs <- cur
		}
		close(jobs)
		wg.Wait()
	}

	// Day 1: open pendings, and some history.
	var day1 [][]*servicepb.Request
	for i := uint64(0); i < *open0; i++ {
		r := ref("d1", i)
		day1 = append(day1, []*servicepb.Request{createTx(*ledger, pay(r, "pending"), usd("world", holdP+r, amount(i)))})
	}
	for i := uint64(0); i < *n/10; i++ {
		r := ref("h", i)
		day1 = append(day1, []*servicepb.Request{
			createTx(*ledger, pay(r, "pending"), usd("world", holdP+r, amount(i))),
			createTx(*ledger, pay(r, "succeeded"), usd(holdP+r, main, amount(i))),
		})
	}
	run(day1)
	Sprev, Tprev := ledgerStats(ctx, *ledger)
	openPrev := sumOpen(ctx, *ledger, holdP)
	mainPrev := accountPair(ctx, *ledger, main)
	fmt.Printf("day 1 cut: S_prev=%d T_prev=%d; open holds %s; %s at S_prev in=%s out=%s\n", Sprev, Tprev, openPrev, main, mainPrev.in, mainPrev.out)

	// Day 2: the window (T_prev, T].
	var day2 [][]*servicepb.Request
	var truth struct{ silent, drain, payouts, fees, refunds uint64 }
	for i := uint64(0); i < *n; i++ {
		r := ref("p", i)
		day2 = append(day2, []*servicepb.Request{
			createTx(*ledger, pay(r, "pending"), usd("world", holdP+r, amount(i))),
			createTx(*ledger, pay(r, "succeeded"), usd(holdP+r, main, amount(i))),
		})
	}
	for i := uint64(0); i < *settle0; i++ {
		r := ref("d1", i)
		day2 = append(day2, []*servicepb.Request{createTx(*ledger, pay(r, "succeeded"), usd(holdP+r, main, amount(i)))})
	}
	for i := uint64(0); i < *direct; i++ {
		day2 = append(day2, []*servicepb.Request{createTx(*ledger, pay(ref("dir", i), "succeeded"), usd("world", main, amount(i)))})
	}
	for i := uint64(0); i < *silent; i++ {
		day2 = append(day2, []*servicepb.Request{createTx(*ledger, nil, usd("world", main, amount(i)))})
		truth.silent += amount(i)
	}
	for i := uint64(0); i < *drain; i++ {
		// The hold was opened with its key; its final forgot it.
		r := ref("kd", i)
		day2 = append(day2, []*servicepb.Request{
			createTx(*ledger, pay(r, "pending"), usd("world", holdP+r, amount(i))),
			createTx(*ledger, nil, usd(holdP+r, main, amount(i))),
		})
		truth.drain += amount(i)
	}
	for i := uint64(0); i < *payouts; i++ {
		day2 = append(day2, []*servicepb.Request{createTx(*ledger, other("payout", i), usd(main, bank, 50))})
		truth.payouts += 50
	}
	for i := uint64(0); i < *fees; i++ {
		day2 = append(day2, []*servicepb.Request{createTx(*ledger, other("fee", i), usd(main, feeAcc, 3))})
		truth.fees += 3
	}
	for i := uint64(0); i < *refunds; i++ {
		day2 = append(day2, []*servicepb.Request{createTx(*ledger, pay(ref("p", i), "refunded"), usd(main, "world", amount(i)/2))})
		truth.refunds += amount(i) / 2
	}
	t0 := time.Now()
	run(day2)
	S, T := ledgerStats(ctx, *ledger)
	fmt.Printf("day 2 loaded in %s: window (T_prev,T] = (%d,%d], %d transactions\n", time.Since(t0).Round(time.Millisecond), Tprev, T, T-Tprev)
	// Oracle at the cut, for the stock and the payment account only.
	resp, err := apply(ctx, &servicepb.Request{Type: &servicepb.Request_CreateQueryCheckpoint{CreateQueryCheckpoint: &servicepb.CreateQueryCheckpointRequest{}}})
	if err != nil {
		log.Fatalf("checkpoint: %v", err)
	}
	var cp uint64
	for _, l := range resp.GetLogs() {
		if c := l.GetPayload().GetCreatedQueryCheckpoint(); c != nil {
			cp = c.GetCheckpointId()
		}
	}
	if s2, t2 := ledgerStats(ctx, *ledger); s2 != S || t2 != T {
		log.Fatal("a write slipped in between the cut and the checkpoint")
	}

	// After the cut: more activity the rewind must take back out.
	var late [][]*servicepb.Request
	for i := uint64(0); i < *after; i++ {
		r := ref("late", i)
		late = append(late, []*servicepb.Request{
			createTx(*ledger, pay(r, "pending"), usd("world", holdP+r, amount(i))),
			createTx(*ledger, pay(r, "succeeded"), usd(holdP+r, main, amount(i))),
			createTx(*ledger, nil, usd("world", main, 1)),
		})
	}
	for i := uint64(0); i < min(*open0-*settle0, 500); i++ {
		r := ref("d1", *settle0+i)
		late = append(late, []*servicepb.Request{createTx(*ledger, pay(r, "succeeded"), usd(holdP+r, main, amount(*settle0+i)))})
	}
	run(late)
	_, headTx := ledgerStats(ctx, *ledger)

	// --- The run, as recon would do it at the cut (S, T). ---
	// Flow: keyed transactions of the window.
	tf := time.Now()
	flow := keyedWindow(ctx, *ledger, Tprev, T, *ranges, *keyedOther)
	tFlow := time.Since(tf)
	opened, lettered := new(big.Int), new(big.Int)
	flowMain := newPair()
	byClass := map[string]*big.Int{}
	for _, tx := range flow {
		for a, p := range ownNet(tx, func(a string) bool { return strings.HasPrefix(a, holdP) || a == main }) {
			if a == main {
				flowMain.in.Add(flowMain.in, p.in)
				flowMain.out.Add(flowMain.out, p.out)
				continue
			}
			b := p.balance()
			if b.Sign() > 0 {
				opened.Add(opened, b)
			} else {
				lettered.Sub(lettered, b)
			}
		}
		class := "payment:" + tx.GetMetadata()["state"].GetStringValue()
		if tx.GetMetadata()["payment_ref"] == nil {
			class = "movement"
		}
		if byClass[class] == nil {
			byClass[class] = new(big.Int)
		}
		byClass[class].Add(byClass[class], big.NewInt(1))
	}

	// Stock at S: live listing + main, rewound with the unfiltered transactions (T, head_tx].
	ts := time.Now()
	liveHolds := map[string]*big.Int{}
	if _, _, _, err := scan(ctx, *ledger, holdP, 0, 1000, func(r row) { liveHolds[holdP+r.key] = r.balance }); err != nil {
		log.Fatalf("scan: %v", err)
	}
	mainLive := accountPair(ctx, *ledger, main)
	wr := readWindow(ctx, *ledger, "txs", T, headTx, *ranges, func(a string) bool { return strings.HasPrefix(a, holdP) || a == main })
	openS := new(big.Int)
	for a, v := range liveHolds {
		if _, touched := wr.first[a]; !touched {
			openS.Add(openS, v)
		}
	}
	for a, v := range wr.first {
		if strings.HasPrefix(a, holdP) {
			openS.Add(openS, v.balance())
		}
	}
	mainS := mainLive
	if p, ok := wr.first[main]; ok {
		mainS = p
	}
	tStock := time.Since(ts)
	oracleOpen := new(big.Int)
	if _, _, _, err := scan(ctx, *ledger, holdP, cp, 1000, func(r row) { oracleOpen.Add(oracleOpen, r.balance) }); err != nil {
		log.Fatalf("oracle scan: %v", err)
	}
	oa, err := svc.GetAccount(ctx, &servicepb.GetAccountRequest{Ledger: *ledger, Address: main, CheckpointId: cp})
	if err != nil {
		log.Fatalf("oracle account: %v", err)
	}
	oracleMain := newPair()
	for _, v := range oa.GetVolumes() {
		if v.GetAsset() == "USD/2" {
			i, _ := new(big.Int).SetString(v.GetVolumes().GetInput(), 10)
			o, _ := new(big.Int).SetString(v.GetVolumes().GetOutput(), 10)
			oracleMain.in.Add(oracleMain.in, i)
			oracleMain.out.Add(oracleMain.out, o)
		}
	}
	cpDelete(ctx, cp)

	// Checks.
	expectOpen := new(big.Int).Add(openPrev, opened)
	expectOpen.Sub(expectOpen, lettered)
	contRes := new(big.Int).Sub(openS, expectOpen)
	dIn := new(big.Int).Sub(mainS.in, mainPrev.in)
	dOut := new(big.Int).Sub(mainS.out, mainPrev.out)
	resIn := new(big.Int).Sub(dIn, flowMain.in)
	resOut := new(big.Int).Sub(dOut, flowMain.out)

	var classes []string
	for c := range byClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	var cs []string
	for _, c := range classes {
		cs = append(cs, fmt.Sprintf("%s=%s", c, byClass[c]))
	}
	fmt.Printf("flow read (T_prev,T] %s: %d keyed transactions [%s] in %s\n", map[bool]string{false: "payment_ref EXISTS", true: "Or(payment_ref, movement_ref) EXISTS"}[*keyedOther], len(flow), strings.Join(cs, " "), tFlow.Round(time.Millisecond))
	fmt.Printf("stock at S: %d live holds, rewind window (T,%d] = %d tx read (count check %v) in %s total; vs oracle: open %s/%s, %s in %s/%s out %s/%s\n", len(liveHolds), headTx, wr.items, wr.items == headTx-T, tStock.Round(time.Millisecond), openS, oracleOpen, main, mainS.in, oracleMain.in, mainS.out, oracleMain.out)
	fmt.Printf("CONTINUITY  open(S)=%s  vs  open(S_prev)=%s + opened=%s - lettered=%s  => residual %s  (expected -%d from keyless drains)\n", openS, openPrev, opened, lettered, contRes, truth.drain)
	fmt.Printf("BOOK %s credits: in(S)-in(S_prev)=%s vs flow credits %s => residual %s  (truth: silent %d + keyless drains %d = %d)\n", main, dIn, flowMain.in, resIn, truth.silent, truth.drain, truth.silent+truth.drain)
	fmt.Printf("BOOK %s debits:  out(S)-out(S_prev)=%s vs flow debits %s => residual %s  (truth: payouts %d + fees %d%s)\n", main, dOut, flowMain.out, resOut, truth.payouts, truth.fees, map[bool]string{false: ", unkeyed", true: ", keyed: expected 0"}[*keyedOther])
}

// keyedWindow returns the window's transactions that carry the key, read
// over k id ranges.
func keyedWindow(ctx context.Context, ledger string, lo, hi uint64, k int, withOther bool) []*commonpb.Transaction {
	exists := func(key string) *commonpb.QueryFilter {
		return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Field{Field: &commonpb.FieldCondition{
			Field: &commonpb.FieldRef{Metadata: key}, Condition: &commonpb.FieldCondition_ExistsCond{ExistsCond: &commonpb.ExistsCondition{}},
		}}}
	}
	membership := exists("payment_ref")
	if withOther {
		membership = &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Or{Or: &commonpb.OrFilter{Filters: []*commonpb.QueryFilter{exists("payment_ref"), exists("movement_ref")}}}}
	}
	span := (hi - lo + uint64(k) - 1) / uint64(k)
	parts := make([][]*commonpb.Transaction, k)
	var wg sync.WaitGroup
	for r := 0; r < k; r++ {
		rlo := lo + uint64(r)*span
		rhi := min(rlo+span, hi)
		if rlo >= rhi {
			continue
		}
		wg.Add(1)
		go func(r int, rlo, rhi uint64) {
			defer wg.Done()
			filter := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: []*commonpb.QueryFilter{
				{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
					Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID, Cond: &commonpb.UintCondition{Min: &rlo, MinExclusive: true, Max: &rhi},
				}}},
				membership,
			}}}}
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
					parts[r] = append(parts[r], tx)
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
	var all []*commonpb.Transaction
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

func sumOpen(ctx context.Context, ledger, prefix string) *big.Int {
	total := new(big.Int)
	if _, _, _, err := scan(ctx, ledger, prefix, 0, 1000, func(r row) { total.Add(total, r.balance) }); err != nil {
		log.Fatalf("scan: %v", err)
	}
	return total
}

func accountPair(ctx context.Context, ledger, addr string) pair {
	a, err := svc.GetAccount(ctx, &servicepb.GetAccountRequest{Ledger: ledger, Address: addr})
	if err != nil {
		log.Fatalf("get account %s: %v", addr, err)
	}
	p := newPair()
	for _, v := range a.GetVolumes() {
		if v.GetAsset() != "USD/2" {
			continue
		}
		if in, ok := new(big.Int).SetString(v.GetVolumes().GetInput(), 10); ok {
			p.in.Add(p.in, in)
		}
		if out, ok := new(big.Int).SetString(v.GetVolumes().GetOutput(), 10); ok {
			p.out.Add(p.out, out)
		}
	}
	return p
}

// crossover measures the two ways to get a day's stock at its cut, to find where
// one overtakes the other (design doc §7.8):
//
//   - list + rewind: a live listing of the N open holds, then a fold of the
//     transactions written since the cut, w = since × X;
//   - forward: read the previous day's stored stock (N rows), then fold the day's
//     X transactions onto it.
//
// It times listings of growing hex sub-prefixes (the addresses are uniform), folds
// of the last X transactions, and the decode and merge of a stored stock of N rows
// (gzipped NDJSON, in memory: the object-storage read is left out). Run it with no
// writes. Each figure is the best of -reps runs.
func crossover(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("crossover", flag.ExitOnError)
	ledger := fs.String("ledger", "psp", "")
	prefix := fs.String("prefix", "psp:tx:", "")
	ranges := fs.Int("ranges", 8, "concurrent id ranges for the folds")
	sizesArg := fs.String("sizes", "10000,50000,100000,250000,500000,1000000", "fold window sizes X")
	days := fs.String("days", "100000,1000000", "transactions per day for the crossover table")
	since := fs.Float64("since", 2.0/24, "share of the day written between the cut-off and the run")
	reps := fs.Int("reps", 2, "")
	_ = fs.Parse(args)
	_, head := ledgerStats(ctx, *ledger)

	// Listings: the open book is the accounts under a set of sub-prefixes.
	type listing struct {
		name string
		subs []string
	}
	hex := "0123456789abcdef"
	sets := []listing{{"000", []string{"000"}}, {"00", []string{"00"}}, {"0", []string{"0"}}}
	for _, k := range []int{2, 4, 8} {
		var subs []string
		for i := 0; i < k; i++ {
			subs = append(subs, hex[i:i+1])
		}
		sets = append(sets, listing{fmt.Sprintf("0-%c", hex[k-1]), subs})
	}
	sets = append(sets, listing{"all", []string{""}})
	type point struct {
		n   float64
		sec float64
	}
	var lists []point
	var all []row
	fmt.Printf("ledger %s, head tx %d, fold K=%d, best of %d\n\nlive listing, one stream, page 1000:\n", *ledger, head, *ranges, *reps)
	for _, s := range sets {
		best := time.Duration(0)
		n := 0
		for r := 0; r < *reps; r++ {
			var rows []row
			n = 0
			t0 := time.Now()
			for _, sub := range s.subs {
				c, _, _, err := scan(ctx, *ledger, *prefix+sub, 0, 1000, func(x row) {
					if s.name == "all" {
						rows = append(rows, x)
					}
				})
				if err != nil {
					log.Fatalf("scan: %v", err)
				}
				n += c
			}
			el := time.Since(t0)
			if best == 0 || el < best {
				best = el
			}
			if s.name == "all" {
				all = rows
			}
		}
		lists = append(lists, point{float64(n), best.Seconds()})
		fmt.Printf("  %-6s %8d accounts  %8s  %6.0f/s\n", s.name, n, best.Round(time.Millisecond), float64(n)/best.Seconds())
	}

	// Folds of the last X transactions.
	var folds []point
	fmt.Printf("\nfold of the unfiltered transactions (head-X, head], reverse=true, K=%d:\n", *ranges)
	for _, f := range strings.Split(*sizesArg, ",") {
		var x uint64
		scanFlag(f, &x)
		if x > head {
			x = head
		}
		best := time.Duration(0)
		var wr windowRead
		for r := 0; r < *reps; r++ {
			wr = readWindow(ctx, *ledger, "txs", head-x, head, *ranges, prefixMatch(*prefix))
			if best == 0 || wr.elapsed < best {
				best = wr.elapsed
			}
		}
		folds = append(folds, point{float64(x), best.Seconds()})
		fmt.Printf("  X=%8d  %8s  %6.0f/s  (%d accounts touched, count check %v)\n", x, best.Round(time.Millisecond), float64(x)/best.Seconds(), len(wr.last), wr.items == x)
	}

	// Stored stock: decode N rows of gzipped NDJSON and merge a fold onto them.
	var stocks []point
	fmt.Printf("\nstored stock, gzipped NDJSON decoded and merged in memory:\n")
	merge := readWindow(ctx, *ledger, "txs", head-min(head, 100000), head, *ranges, prefixMatch(*prefix)).last
	for _, lp := range lists {
		n := int(lp.n)
		if n > len(all) {
			n = len(all)
		}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		enc := json.NewEncoder(zw)
		for _, x := range all[:n] {
			_ = enc.Encode(map[string]string{"side": "psp", "hold": *prefix + x.key, "asset": "USD/2", "balance": x.balance.String()})
		}
		_ = zw.Close()
		best := time.Duration(0)
		for r := 0; r < *reps; r++ {
			t0 := time.Now()
			zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
			if err != nil {
				log.Fatal(err)
			}
			stock := make(map[string]*big.Int, n)
			dec := json.NewDecoder(zr)
			for {
				var o struct{ Hold, Balance string }
				if err := dec.Decode(&o); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					log.Fatal(err)
				}
				b, _ := new(big.Int).SetString(o.Balance, 10)
				stock[o.Hold] = b
			}
			for a, p := range merge {
				if b := p.balance(); b.Sign() == 0 {
					delete(stock, a)
				} else {
					stock[a] = b
				}
			}
			if el := time.Since(t0); best == 0 || el < best {
				best = el
			}
		}
		stocks = append(stocks, point{float64(n), best.Seconds()})
		fmt.Printf("  N=%8d  %6.1f MB gz  %8s\n", n, float64(buf.Len())/1e6, best.Round(time.Millisecond))
	}

	// Piecewise-linear interpolation through measured points, extrapolated at the ends.
	interp := func(ps []point, v float64) float64 {
		sort.Slice(ps, func(i, j int) bool { return ps[i].n < ps[j].n })
		i := sort.Search(len(ps), func(i int) bool { return ps[i].n >= v })
		switch {
		case i == 0:
			i = 1
		case i == len(ps):
			i = len(ps) - 1
		}
		a, b := ps[i-1], ps[i]
		return a.sec + (b.sec-a.sec)*(v-a.n)/(b.n-a.n)
	}
	fmt.Printf("\ncrossover (the run starts after %.0f%% of the next day is written):\n", *since*100)
	for _, d := range strings.Split(*days, ",") {
		var x float64
		scanFlag(d, &x)
		w := *since * x
		fmt.Printf("  X_day=%.0f, w=%.0f:\n", x, w)
		var prevN, prevDiff float64
		found := false
		for _, lp := range lists {
			n := lp.n
			lr := lp.sec + interp(folds, w)
			fw := interp(stocks, n) + interp(folds, x)
			diff := lr - fw
			fmt.Printf("    N_open=%8.0f  list+rewind %7.2f s  forward %7.2f s  -> %s\n", n, lr, fw, map[bool]string{true: "forward", false: "list+rewind"}[diff > 0])
			if !found && prevN > 0 && prevDiff <= 0 && diff > 0 {
				nStar := prevN + (n-prevN)*(-prevDiff)/(diff-prevDiff)
				fmt.Printf("    crossover at N_open ≈ %.0f (%.1f%% of the day's transactions)\n", nStar, 100*nStar/x)
				found = true
			}
			prevN, prevDiff = n, diff
		}
		if !found {
			fmt.Printf("    no crossover in the measured range\n")
		}
	}
}
