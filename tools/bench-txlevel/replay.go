package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
)

// replayRewind rewinds the live listing of a prefix to the transaction id -from,
// as the replay of an old day does from head: the window (from, head] can span
// months. It folds that window two ways and compares their stocks:
//   - asc: in id order, keeping each account's first touch (readWindow), so it
//     holds every account touched in the window;
//   - desc: newest first, in chunks read by -ranges workers and applied in
//     order, each touch overwriting the account's balance before it, and an
//     account back at zero dropped unless the listing holds it. It holds only
//     the accounts open at the point it has read down to.
//
// It reports each fold's time and peak heap.
func replayRewind(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("replay-rewind", flag.ExitOnError)
	ledger := fs.String("ledger", "replay", "")
	prefix := fs.String("prefix", "psp:hold:", "")
	from := fs.Uint64("from", 0, "the cut T: rewind to this transaction id")
	to := fs.Uint64("to", 0, "0 = head")
	ranges := fs.Int("ranges", 8, "")
	chunk := fs.Uint64("chunk", 100_000, "transactions per chunk (desc)")
	mode := fs.String("mode", "both", "asc, desc or both")
	_ = fs.Parse(args)
	if *to == 0 {
		_, *to = ledgerStats(ctx, *ledger)
	}
	in := prefixMatch(*prefix)

	listed := map[string]*big.Int{}
	t0 := time.Now()
	if _, _, _, err := scan(ctx, *ledger, *prefix, 0, 1000, func(r row) { listed[*prefix+r.key] = r.balance }); err != nil {
		log.Fatalf("scan: %v", err)
	}
	fmt.Printf("listing %s: %d open accounts in %s; window (%d,%d] = %d transactions\n",
		*prefix, len(listed), time.Since(t0).Round(time.Millisecond), *from, *to, *to-*from)

	var asc, desc map[string]*big.Int
	if *mode == "asc" || *mode == "both" {
		stop, peak := heapPeak()
		wr := readWindow(ctx, *ledger, "txs", *from, *to, *ranges, in)
		stop()
		asc = map[string]*big.Int{}
		for a, v := range listed {
			asc[a] = v
		}
		for a, p := range wr.first {
			if b := p.balance(); b.Sign() != 0 {
				asc[a] = b
			} else {
				delete(asc, a)
			}
		}
		fmt.Printf("asc:  %d rows (count check %v), %s (%.0f tx/s), %d accounts held, peak heap %s, stock %d rows\n",
			wr.items, wr.items == *to-*from, wr.elapsed.Round(time.Millisecond), float64(wr.items)/wr.elapsed.Seconds(),
			len(wr.first), mib(*peak), len(asc))
		wr = windowRead{}
		runtime.GC()
	}
	if *mode == "desc" || *mode == "both" {
		stop, peak := heapPeak()
		var rows atomic.Uint64
		held := 0
		t1 := time.Now()
		desc, held = rewindDesc(ctx, *ledger, *from, *to, *chunk, *ranges, in, listed, &rows)
		elapsed := time.Since(t1)
		stop()
		fmt.Printf("desc: %d rows (count check %v), %s (%.0f tx/s), at most %d accounts held, peak heap %s, stock %d rows\n",
			rows.Load(), rows.Load() == *to-*from, elapsed.Round(time.Millisecond), float64(rows.Load())/elapsed.Seconds(),
			held, mib(*peak), len(desc))
	}
	if asc != nil && desc != nil {
		diffs := 0
		for a, v := range asc {
			if w, ok := desc[a]; !ok || w.Cmp(v) != 0 {
				diffs++
			}
		}
		for a := range desc {
			if _, ok := asc[a]; !ok {
				diffs++
			}
		}
		fmt.Printf("asc vs desc: %d rows differ\n", diffs)
	}
}

// rewindDesc returns the stock at the transaction id lo: the listing, with every
// account touched in (lo, hi] set to its balance just before its first touch.
func rewindDesc(ctx context.Context, ledger string, lo, hi, chunk uint64, k int, in match,
	listed map[string]*big.Int, rows *atomic.Uint64) (map[string]*big.Int, int) {
	n := int((hi - lo + chunk - 1) / chunk)
	type result struct {
		j   int
		pre map[string]*big.Int
	}
	results := make(chan result, 2*k)
	tokens := make(chan struct{}, 2*k) // chunks read but not applied yet
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < k; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				tokens <- struct{}{}
				j := int(next.Add(1) - 1)
				if j >= n {
					<-tokens
					return
				}
				chi := hi - uint64(j)*chunk
				clo := lo
				if chi-lo > chunk {
					clo = chi - chunk
				}
				// Newest first within the chunk: the last write per account is
				// its earliest touch in the chunk.
				pre := map[string]*big.Int{}
				cond := &commonpb.UintCondition{Min: &clo, MinExclusive: true, Max: &chi}
				var cursor string
				for {
					stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor,
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
						rows.Add(1)
						var net map[string]pair
						for addr, vba := range tx.GetPostCommitVolumes().GetVolumesByAccount() {
							if !in(addr) {
								continue
							}
							if net == nil {
								net = ownNet(tx, in)
							}
							post := pcvPair(vba)
							own, ok := net[addr]
							if !ok {
								own = newPair()
							}
							pre[addr] = pair{new(big.Int).Sub(post.in, own.in), new(big.Int).Sub(post.out, own.out)}.balance()
						}
					}
					vals := stream.Trailer().Get("x-next-cursor")
					if len(vals) == 0 || vals[0] == "" {
						break
					}
					cursor = vals[0]
				}
				results <- result{j, pre}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	stock := map[string]*big.Int{}
	pending := map[int]map[string]*big.Int{}
	applied, held := 0, 0
	for r := range results {
		pending[r.j] = r.pre
		for {
			pre, ok := pending[applied]
			if !ok {
				break
			}
			delete(pending, applied)
			// An older chunk overrides a newer one. A balance of zero before the
			// touch drops the account, unless the listing holds it: then zero
			// overrides the listed value.
			for a, v := range pre {
				if v.Sign() != 0 {
					stock[a] = v
				} else if _, ok := listed[a]; ok {
					stock[a] = v
				} else {
					delete(stock, a)
				}
			}
			held = max(held, len(stock))
			applied++
			<-tokens
		}
	}
	out := map[string]*big.Int{}
	for a, v := range listed {
		out[a] = v
	}
	for a, v := range stock {
		if v.Sign() != 0 {
			out[a] = v
		} else {
			delete(out, a)
		}
	}
	return out, held
}

// heapPeak samples the heap in use until stop is called.
func heapPeak() (stop func(), peak *uint64) {
	peak = new(uint64)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			runtime.ReadMemStats(&ms)
			*peak = max(*peak, ms.HeapInuse)
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	return func() { close(done); wg.Wait() }, peak
}

func mib(b uint64) string { return fmt.Sprintf("%d MiB", b>>20) }
