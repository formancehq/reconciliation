// Command bench-txlevel reproduces the measurements behind ADR-005
// (docs/prd/adr-005-transaction-level-reconciliation.md): it loads two ledgers
// with 1-to-1 keyed accounts, then times aggregate, full-scan, keyed-diff and
// log-window reads, live and at a query checkpoint, and proves the rewind of a
// live listing to a log-id cut. It is a benchmark, not part of the service:
// run it against a throwaway ledger only (see README.md) — it writes millions of
// transactions and creates query checkpoints.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/grpcprotocol"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var svc servicepb.BucketServiceClient

func main() {
	addr := os.Getenv("LEDGER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18888"
	}
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpcprotocol.ClientOption(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(256<<20), grpc.MaxCallSendMsgSize(256<<20)),
	)
	if err != nil {
		log.Fatal(err)
	}
	svc = servicepb.NewBucketServiceClient(conn)

	if len(os.Args) < 2 {
		log.Fatal("usage: bench-txlevel load|load-mixed|retag|cp-create|cp-delete|agg|scan|diff|logs|txs|probe|cutprobe|lastlog|rewind ...")
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx := context.Background()
	switch cmd {
	case "load":
		load(ctx, args)
	case "cp-create":
		cpCreate(ctx)
	case "cp-delete":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		id := fs.Uint64("id", 0, "")
		_ = fs.Parse(args)
		cpDelete(ctx, *id)
	case "agg":
		agg(ctx, args)
	case "scan":
		scanCmd(ctx, args)
	case "diff":
		diff(ctx, args)
	case "logs":
		logsCmd(ctx, args)
	case "probe":
		probe(ctx)
	case "load-mixed":
		loadMixed(ctx, args)
	case "txs":
		txsCmd(ctx, args)
	case "retag":
		retag(ctx, args)
	case "cutprobe":
		cutProbe(ctx)
	case "lastlog":
		if len(args) != 1 {
			log.Fatal("usage: bench-txlevel lastlog <ledger>")
		}
		fmt.Println(lastLogID(ctx, args[0]))
	case "rewind":
		rewind(ctx, args)
	default:
		log.Fatalf("unknown %s", cmd)
	}
}

// id returns a pseudo-random 16-hex-char "PSP transaction id" for i, so the
// address order is unrelated to the insertion order, as with real PSP ids.
func id(i uint64) string {
	z := i + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	return fmt.Sprintf("%016x", z)
}

func amount(i uint64) uint64 { return 100 + (i*7919)%100000 }

func ensureLedger(ctx context.Context, name string) {
	_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{
		Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
			Name: name, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
		}}}},
	}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create ledger %s: %v", name, err)
	}
}

// load writes one world -> {prefix}{id(i)} transaction per i in [start, start+n).
// With -drift, every 1000th id is skipped (missing on this side), every 1000th+1
// id gets amount+1 (mismatch), and every 1000th+2 is written twice (duplicate).
func load(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	ledger := fs.String("ledger", "", "")
	prefix := fs.String("prefix", "", "")
	n := fs.Uint64("n", 1000, "")
	start := fs.Uint64("start", 0, "")
	batch := fs.Int("batch", 200, "")
	workers := fs.Int("workers", 16, "")
	drift := fs.Bool("drift", false, "")
	_ = fs.Parse(args)
	ensureLedger(ctx, *ledger)

	jobs := make(chan []uint64, *workers*2)
	var done atomic.Uint64
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ids := range jobs {
				reqs := make([]*servicepb.Request, 0, len(ids))
				for _, i := range ids {
					amt := amount(i)
					if *drift {
						switch i % 1000 {
						case 0:
							continue
						case 1:
							amt++
						}
					}
					reqs = append(reqs, &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
						Ledger: *ledger,
						Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
							Postings: []*commonpb.Posting{{Source: "world", Destination: *prefix + id(i), Amount: commonpb.NewUint256FromUint64(amt), Asset: "USD/2"}},
							Force:    true,
						}}},
					}}})
				}
				for attempt := 0; ; attempt++ {
					_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}})
					if err == nil {
						break
					}
					if attempt > 20 {
						log.Fatalf("apply: %v", err)
					}
					time.Sleep(200 * time.Millisecond)
				}
				d := done.Add(uint64(len(reqs)))
				if d%100000 < uint64(len(reqs)) {
					log.Printf("%s: %d tx (%.0f tx/s)", *ledger, d, float64(d)/time.Since(t0).Seconds())
				}
			}
		}()
	}
	cur := make([]uint64, 0, *batch)
	for i := *start; i < *start+*n; i++ {
		cur = append(cur, i)
		if *drift && i%1000 == 2 {
			// A duplicate funding on the same account: its balance doubles.
			cur = append(cur, i)
		}
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
	fmt.Printf("loaded %d tx on %s in %s (%.0f tx/s)\n", done.Load(), *ledger, el.Round(time.Millisecond), float64(done.Load())/el.Seconds())
}

func cpCreate(ctx context.Context) {
	t0 := time.Now()
	resp, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{
		Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateQueryCheckpoint{CreateQueryCheckpoint: &servicepb.CreateQueryCheckpointRequest{}}}},
	}}})
	if err != nil {
		log.Fatalf("create checkpoint: %v", err)
	}
	for _, l := range resp.GetLogs() {
		if cp := l.GetPayload().GetCreatedQueryCheckpoint(); cp != nil {
			fmt.Printf("checkpoint %d (max_sequence %d) ready in %s\n", cp.GetCheckpointId(), cp.GetMaxSequence(), time.Since(t0).Round(time.Millisecond))
			return
		}
	}
	log.Fatal("no checkpoint log in response")
}

func cpDelete(ctx context.Context, cpID uint64) {
	t0 := time.Now()
	_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{
		Requests: []*servicepb.Request{{Type: &servicepb.Request_DeleteQueryCheckpoint{DeleteQueryCheckpoint: &servicepb.DeleteQueryCheckpointRequest{CheckpointId: cpID}}}},
	}}})
	if err != nil {
		log.Fatalf("delete checkpoint: %v", err)
	}
	fmt.Printf("checkpoint %d deleted in %s\n", cpID, time.Since(t0).Round(time.Millisecond))
}

func prefixFilter(prefix string) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Address{Address: &commonpb.AddressMatch{
		Match: &commonpb.AddressMatch_HardcodedPrefix{HardcodedPrefix: prefix},
		Role:  commonpb.AddressRole_ADDRESS_ROLE_ANY,
	}}}
}

func agg(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("agg", flag.ExitOnError)
	ledger := fs.String("ledger", "", "")
	prefix := fs.String("prefix", "", "")
	cp := fs.Uint64("cp", 0, "")
	_ = fs.Parse(args)
	t0 := time.Now()
	resp, err := svc.AggregateVolumes(ctx, &servicepb.AggregateVolumesRequest{Ledger: *ledger, Filter: prefixFilter(*prefix), CheckpointId: *cp, CollapseColors: true})
	if err != nil {
		log.Fatalf("aggregate: %v", err)
	}
	for _, v := range resp.GetVolumes() {
		b := new(big.Int).Sub(v.GetInput().ToBigInt(), v.GetOutput().ToBigInt())
		fmt.Printf("agg %s %s cp=%d: %s %s in %s\n", *ledger, *prefix, *cp, v.GetAsset(), b, time.Since(t0).Round(time.Millisecond))
	}
}

type row struct {
	key     string
	balance *big.Int
}

// scan streams every account under prefix following the cursor, returning the
// rows keyed by the address suffix, and whether they arrived in key order.
func scan(ctx context.Context, ledger, prefix string, cp uint64, page uint32, fn func(row)) (n int, sorted bool, pages int, err error) {
	var cursor, last string
	sorted = true
	for {
		stream, err := svc.ListAccounts(ctx, &servicepb.ListAccountsRequest{Ledger: ledger, Options: &commonpb.ListOptions{
			Read: &commonpb.ReadOptions{CheckpointId: cp}, PageSize: page, Cursor: cursor, Filter: prefixFilter(prefix),
		}})
		if err != nil {
			return n, sorted, pages, err
		}
		pages++
		for {
			a, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				return n, sorted, pages, rerr
			}
			key := strings.TrimPrefix(a.GetAddress(), prefix)
			if key < last {
				sorted = false
			}
			last = key
			bal := new(big.Int)
			for _, v := range a.GetVolumes() {
				if v.GetAsset() == "USD/2" {
					b, _ := new(big.Int).SetString(v.GetVolumes().GetBalance(), 10)
					if b != nil {
						bal.Add(bal, b)
					}
				}
			}
			n++
			fn(row{key: key, balance: bal})
		}
		md := stream.Trailer()
		cursor = ""
		if vals := md.Get("x-next-cursor"); len(vals) > 0 {
			cursor = vals[0]
		}
		if cursor == "" {
			return n, sorted, pages, nil
		}
	}
}

func scanCmd(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	ledger := fs.String("ledger", "", "")
	prefix := fs.String("prefix", "", "")
	cp := fs.Uint64("cp", 0, "")
	page := fs.Uint("page", 1000, "")
	_ = fs.Parse(args)
	t0 := time.Now()
	n, sorted, pages, err := scan(ctx, *ledger, *prefix, *cp, uint32(*page), func(row) {})
	if err != nil {
		log.Fatalf("scan: %v", err)
	}
	el := time.Since(t0)
	fmt.Printf("scan %s cp=%d page=%d: %d accounts, %d pages, sorted=%v in %s (%.0f acc/s)\n", *ledger, *cp, *page, n, pages, sorted, el.Round(time.Millisecond), float64(n)/el.Seconds())
}

type breakRow struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	A     string `json:"a,omitempty"`
	B     string `json:"b,omitempty"`
	Drift string `json:"drift"`
}

// diff scans both sides concurrently into memory, then joins by key and writes
// every break as gzipped NDJSON — the detailed result an async job would upload.
func diff(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	la := fs.String("a", "", "")
	pa := fs.String("pa", "", "")
	lb := fs.String("b", "", "")
	pb := fs.String("pb", "", "")
	cp := fs.Uint64("cp", 0, "")
	page := fs.Uint("page", 1000, "")
	out := fs.String("out", "breaks.ndjson.gz", "")
	seq := fs.Bool("seq", false, "scan A then B (concurrent reads of one checkpoint fail on one node)")
	_ = fs.Parse(args)

	t0 := time.Now()
	var ra, rb []row
	var wg sync.WaitGroup
	var errA, errB error
	var sa, sb bool
	var ta, tb time.Duration
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := time.Now()
		_, sa, _, errA = scan(ctx, *la, *pa, *cp, uint32(*page), func(r row) { ra = append(ra, r) })
		ta = time.Since(s)
	}()
	if *seq {
		wg.Wait()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := time.Now()
		_, sb, _, errB = scan(ctx, *lb, *pb, *cp, uint32(*page), func(r row) { rb = append(rb, r) })
		tb = time.Since(s)
	}()
	wg.Wait()
	if errA != nil || errB != nil {
		log.Fatalf("scan: %v %v", errA, errB)
	}
	tScan := time.Since(t0)

	t1 := time.Now()
	if !sa {
		sort.Slice(ra, func(i, j int) bool { return ra[i].key < ra[j].key })
	}
	if !sb {
		sort.Slice(rb, func(i, j int) bool { return rb[i].key < rb[j].key })
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	bw := bufio.NewWriter(gz)
	enc := json.NewEncoder(bw)
	var matched, mismatch, onlyA, onlyB int
	totA, totB := new(big.Int), new(big.Int)
	absDrift := new(big.Int)
	emit := func(b breakRow, d *big.Int) {
		absDrift.Add(absDrift, new(big.Int).Abs(d))
		_ = enc.Encode(b)
	}
	i, j := 0, 0
	for i < len(ra) || j < len(rb) {
		switch {
		case j >= len(rb) || (i < len(ra) && ra[i].key < rb[j].key):
			totA.Add(totA, ra[i].balance)
			onlyA++
			emit(breakRow{Key: ra[i].key, Kind: "missing_in_b", A: ra[i].balance.String(), Drift: ra[i].balance.String()}, ra[i].balance)
			i++
		case i >= len(ra) || rb[j].key < ra[i].key:
			totB.Add(totB, rb[j].balance)
			onlyB++
			d := new(big.Int).Neg(rb[j].balance)
			emit(breakRow{Key: rb[j].key, Kind: "missing_in_a", B: rb[j].balance.String(), Drift: d.String()}, d)
			j++
		default:
			totA.Add(totA, ra[i].balance)
			totB.Add(totB, rb[j].balance)
			if ra[i].balance.Cmp(rb[j].balance) == 0 {
				matched++
			} else {
				mismatch++
				d := new(big.Int).Sub(ra[i].balance, rb[j].balance)
				emit(breakRow{Key: ra[i].key, Kind: "amount_mismatch", A: ra[i].balance.String(), B: rb[j].balance.String(), Drift: d.String()}, d)
			}
			i++
			j++
		}
	}
	_ = bw.Flush()
	_ = gz.Close()
	_ = f.Close()
	tJoin := time.Since(t1)
	st, _ := os.Stat(*out)
	fmt.Printf("diff cp=%d page=%d: A=%d rows (%s, sorted=%v) B=%d rows (%s, sorted=%v)\n", *cp, *page, len(ra), ta.Round(time.Millisecond), sa, len(rb), tb.Round(time.Millisecond), sb)
	fmt.Printf("  matched=%d mismatch=%d missing_in_b=%d missing_in_a=%d | ΣA=%s ΣB=%s net=%s |abs|=%s\n", matched, mismatch, onlyA, onlyB, totA, totB, new(big.Int).Sub(totA, totB), absDrift)
	fmt.Printf("  scan(parallel)=%s join+write=%s total=%s result=%s (%d bytes)\n", tScan.Round(time.Millisecond), tJoin.Round(time.Millisecond), time.Since(t0).Round(time.Millisecond), *out, st.Size())
}

// logsCmd folds the ledger's log stream in (from, to] into the latest
// post-commit balance of every touched account under prefix: the incremental,
// checkpoint-free read of a cut defined by a log id.
func logsCmd(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	ledger := fs.String("ledger", "", "")
	prefix := fs.String("prefix", "", "")
	from := fs.Uint64("from", 0, "exclusive lower log id")
	to := fs.Uint64("to", 0, "inclusive upper log id (0 = head)")
	page := fs.Uint("page", 1000, "")
	_ = fs.Parse(args)
	cond := &commonpb.UintCondition{Min: from, MinExclusive: true}
	if *to > 0 {
		cond.Max = to
	}
	filter := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_LogId{LogId: &commonpb.LogIdCondition{Cond: cond}}}
	state := map[string]*big.Int{}
	t0 := time.Now()
	var cursor string
	var nLogs, pages int
	var lastID uint64
	for {
		stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: uint32(*page), Cursor: cursor, Filter: filter}})
		if err != nil {
			log.Fatalf("list logs: %v", err)
		}
		pages++
		for {
			l, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				log.Fatalf("recv log: %v", rerr)
			}
			nLogs++
			ll := l.GetPayload().GetApply().GetLog()
			if ll.GetId() < lastID {
				log.Printf("non-ascending log order: %d after %d", ll.GetId(), lastID)
			}
			lastID = ll.GetId()
			tx := ll.GetData().GetCreatedTransaction().GetTransaction()
			if tx == nil {
				tx = ll.GetData().GetRevertedTransaction().GetRevertTransaction()
			}
			for addr, vba := range tx.GetPostCommitVolumes().GetVolumesByAccount() {
				if !strings.HasPrefix(addr, *prefix) {
					continue
				}
				bal := new(big.Int)
				for _, v := range vba.GetVolumes() {
					if v.GetAsset() == "USD/2" {
						in, _ := new(big.Int).SetString(v.GetVolumes().GetInput(), 10)
						out, _ := new(big.Int).SetString(v.GetVolumes().GetOutput(), 10)
						if in != nil && out != nil {
							bal.Add(bal, new(big.Int).Sub(in, out))
						}
					}
				}
				state[strings.TrimPrefix(addr, *prefix)] = bal
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
	el := time.Since(t0)
	fmt.Printf("logs %s (%d,%d]: %d logs, %d pages, %d accounts folded, last id %d in %s (%.0f logs/s)\n", *ledger, *from, *to, nLogs, pages, len(state), lastID, el.Round(time.Millisecond), float64(nLogs)/el.Seconds())
}

// probe checks what a lettered (purged) EPHEMERAL hold leaves behind.
func probe(ctx context.Context) {
	ledger := fmt.Sprintf("lett-%d", time.Now().Unix())
	_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{
		Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
			Name: ledger, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
			AccountTypes: map[string]*commonpb.AccountType{"hold": {Name: "hold", Pattern: "hold:{id}", Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL}},
		}}}},
	}}})
	if err != nil {
		log.Fatalf("create: %v", err)
	}
	tx := func(src, dst, ref string, amt uint64) {
		_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
			Ledger: ledger,
			Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
				Postings:  []*commonpb.Posting{{Source: src, Destination: dst, Amount: commonpb.NewUint256FromUint64(amt), Asset: "USD/2"}},
				Force:     true,
				Reference: ref,
			}}},
		}}}}}}})
		if err != nil {
			log.Fatalf("tx %s: %v", ref, err)
		}
	}
	for _, ix := range []commonpb.TransactionBuiltinIndex{commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ADDRESS, commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_REFERENCE, commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_DESTINATION_ADDRESS, commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_SOURCE_ADDRESS} {
		_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{Ledger: ledger, Id: &commonpb.IndexID{Kind: &commonpb.IndexID_TxBuiltin{TxBuiltin: ix}}}}}}}}})
		if err != nil {
			log.Fatalf("index %v: %v", ix, err)
		}
	}
	tx("world", "hold:dep_1", "dep_1:hold", 100) // open
	tx("hold:dep_1", "main", "dep_1:done", 100)  // letter -> purge
	tx("world", "hold:dep_2", "dep_2:hold", 50)  // stays open
	time.Sleep(2 * time.Second)
	tx("world", "hold:dep_3", "dep_3:hold", 70)
	time.Sleep(2 * time.Second)

	list := func(label string, f *commonpb.QueryFilter) {
		stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 100, Filter: f}})
		if err != nil {
			log.Fatalf("%s: %v", label, err)
		}
		var out []string
		for {
			t, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				out = append(out, "ERR "+rerr.Error())
				break
			}
			pcv := []string{}
			for a, v := range t.GetPostCommitVolumes().GetVolumesByAccount() {
				for _, e := range v.GetVolumes() {
					pcv = append(pcv, fmt.Sprintf("%s=%s-%s", a, e.GetVolumes().GetInput(), e.GetVolumes().GetOutput()))
				}
			}
			sort.Strings(pcv)
			out = append(out, fmt.Sprintf("tx%d ref=%s pcv=%v", t.GetId(), t.GetReference(), pcv))
		}
		fmt.Printf("%s -> %v\n", label, out)
	}
	exact := func(a string) *commonpb.QueryFilter {
		return &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Address{Address: &commonpb.AddressMatch{Match: &commonpb.AddressMatch_HardcodedExact{HardcodedExact: a}, Role: commonpb.AddressRole_ADDRESS_ROLE_ANY}}}
	}
	list("all txs", nil)
	list("txs by address hold:dep_1", exact("hold:dep_1"))
	list("txs by address hold:dep_2 (open)", exact("hold:dep_2"))
	list("txs by address main", exact("main"))
	list("BEFORE letter: txs by address hold:dep_3", exact("hold:dep_3"))
	tx("hold:dep_3", "main", "dep_3:done", 70)
	time.Sleep(2 * time.Second)
	list("AFTER letter: txs by address hold:dep_3", exact("hold:dep_3"))
	list("txs by source hold:dep_1", &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Address{Address: &commonpb.AddressMatch{Match: &commonpb.AddressMatch_HardcodedExact{HardcodedExact: "hold:dep_1"}, Role: commonpb.AddressRole_ADDRESS_ROLE_SOURCE}}})
	list("txs by reference dep_1:done", &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_Reference{Reference: &commonpb.ReferenceCondition{Cond: &commonpb.StringCondition{Value: &commonpb.StringCondition_Hardcoded{Hardcoded: "dep_1:done"}}}}})
	var accts []string
	_, _, _, err = scan(ctx, ledger, "hold:", 0, 100, func(r row) { accts = append(accts, r.key+"="+r.balance.String()) })
	fmt.Printf("ListAccounts prefix hold: -> %v (err=%v)\n", accts, err)
	resp, err := svc.AggregateVolumes(ctx, &servicepb.AggregateVolumesRequest{Ledger: ledger, Filter: prefixFilter("hold:"), CollapseColors: true})
	fmt.Printf("AggregateVolumes hold: -> %v (err=%v)\n", resp.GetVolumes(), err)
}

// lastLogID returns the ledger's newest log id: per-ledger log ids are
// contiguous from 1, so the head is the ledger's log count (ListLogs has no
// reverse order to read it directly).
func lastLogID(ctx context.Context, ledger string) uint64 {
	st, err := svc.GetLedgerStats(ctx, &servicepb.GetLedgerStatsRequest{Ledger: ledger})
	if err != nil {
		log.Fatalf("ledger stats: %v", err)
	}
	return st.GetLogCount()
}

func bal(v *commonpb.Volumes) *big.Int {
	in, _ := new(big.Int).SetString(v.GetInput(), 10)
	out, _ := new(big.Int).SetString(v.GetOutput(), 10)
	if in == nil {
		in = new(big.Int)
	}
	if out == nil {
		out = new(big.Int)
	}
	return in.Sub(in, out)
}

// rewind proves that the state of an address-prefixed scope AT log id S can be
// rebuilt without a checkpoint: list the scope live (the listing may tear
// under concurrent writes), then read the logs (S, head] and, for every scope
// account touched there, replace its listed balance by its balance just
// before its first touch after S (post-commit volume minus that
// transaction's own net posting on it). The result is compared, row for row,
// with a checkpoint taken at S.
func rewind(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("rewind", flag.ExitOnError)
	ledger := fs.String("ledger", "psp", "")
	prefix := fs.String("prefix", "psp:tx:", "")
	writers := fs.Int("writers", 8, "")
	_ = fs.Parse(args)

	// 1. The cut: S, and an oracle checkpoint taken with no write in between.
	S := lastLogID(ctx, *ledger)
	resp, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{
		Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateQueryCheckpoint{CreateQueryCheckpoint: &servicepb.CreateQueryCheckpointRequest{}}}},
	}}})
	if err != nil {
		log.Fatalf("checkpoint: %v", err)
	}
	var cp uint64
	for _, l := range resp.GetLogs() {
		if c := l.GetPayload().GetCreatedQueryCheckpoint(); c != nil {
			cp = c.GetCheckpointId()
		}
	}
	if lastLogID(ctx, *ledger) != S {
		log.Fatal("a write slipped in between S and the checkpoint")
	}
	fmt.Printf("cut S=%d, oracle checkpoint %d\n", S, cp)

	// 2. Concurrent writers: top up, fully drain (to zero), and create accounts.
	stop := make(chan struct{})
	var wwg sync.WaitGroup
	var nw atomic.Uint64
	for w := 0; w < *writers; w++ {
		wwg.Add(1)
		go func(w int) {
			defer wwg.Done()
			k := uint64(w) * 1_000_000_000
			for {
				select {
				case <-stop:
					return
				default:
				}
				k++
				i := (k * 2654435761) % 1_000_000
				var p *commonpb.Posting
				switch k % 3 {
				case 0:
					p = &commonpb.Posting{Source: "world", Destination: *prefix + id(i), Amount: commonpb.NewUint256FromUint64(7), Asset: "USD/2"}
				case 1:
					p = &commonpb.Posting{Source: *prefix + id(i), Destination: "sink", Amount: commonpb.NewUint256FromUint64(amount(i)), Asset: "USD/2"}
				default:
					p = &commonpb.Posting{Source: "world", Destination: *prefix + id(5_000_000+k), Amount: commonpb.NewUint256FromUint64(11), Asset: "USD/2"}
				}
				_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
					Ledger: *ledger,
					Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{Postings: []*commonpb.Posting{p}, Force: true}}},
				}}}}}}})
				if err == nil {
					nw.Add(1)
				}
			}
		}(w)
	}

	// 3. Live listing while writes go on.
	t0 := time.Now()
	listed := map[string]*big.Int{}
	if _, _, _, err := scan(ctx, *ledger, *prefix, 0, 1000, func(r row) { listed[r.key] = r.balance }); err != nil {
		log.Fatalf("live scan: %v", err)
	}
	tList := time.Since(t0)
	close(stop)
	wwg.Wait()

	// 4. The correction window (S, head], read after the listing ended.
	t1 := time.Now()
	head := lastLogID(ctx, *ledger)
	firstPre := map[string]*big.Int{}
	var cursor string
	var nLogs int
	cond := &commonpb.UintCondition{Min: &S, MinExclusive: true, Max: &head}
	for {
		stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: 1000, Cursor: cursor,
			Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_LogId{LogId: &commonpb.LogIdCondition{Cond: cond}}}}})
		if err != nil {
			log.Fatalf("logs: %v", err)
		}
		for {
			l, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}
			if rerr != nil {
				log.Fatalf("recv: %v", rerr)
			}
			nLogs++
			ll := l.GetPayload().GetApply().GetLog()
			tx := ll.GetData().GetCreatedTransaction().GetTransaction()
			if tx == nil {
				tx = ll.GetData().GetRevertedTransaction().GetRevertTransaction()
			}
			net := map[string]*big.Int{}
			for _, p := range tx.GetPostings() {
				if p.GetAsset() != "USD/2" {
					continue
				}
				a := p.GetAmount().ToBigInt()
				if strings.HasPrefix(p.GetDestination(), *prefix) {
					k := strings.TrimPrefix(p.GetDestination(), *prefix)
					if net[k] == nil {
						net[k] = new(big.Int)
					}
					net[k].Add(net[k], a)
				}
				if strings.HasPrefix(p.GetSource(), *prefix) {
					k := strings.TrimPrefix(p.GetSource(), *prefix)
					if net[k] == nil {
						net[k] = new(big.Int)
					}
					net[k].Sub(net[k], a)
				}
			}
			for addr, vba := range tx.GetPostCommitVolumes().GetVolumesByAccount() {
				if !strings.HasPrefix(addr, *prefix) {
					continue
				}
				k := strings.TrimPrefix(addr, *prefix)
				if _, seen := firstPre[k]; seen {
					continue
				}
				post := new(big.Int)
				for _, v := range vba.GetVolumes() {
					if v.GetAsset() == "USD/2" {
						post.Add(post, bal(v.GetVolumes()))
					}
				}
				d := net[k]
				if d == nil {
					d = new(big.Int)
				}
				firstPre[k] = new(big.Int).Sub(post, d)
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
	rebuilt := map[string]*big.Int{}
	for k, v := range listed {
		if _, touched := firstPre[k]; !touched {
			rebuilt[k] = v
		}
	}
	for k, v := range firstPre {
		if v.Sign() != 0 {
			rebuilt[k] = v
		}
	}
	tFix := time.Since(t1)

	// 5. Oracle: the scope at the checkpoint.
	oracle := map[string]*big.Int{}
	if _, _, _, err := scan(ctx, *ledger, *prefix, cp, 1000, func(r row) {
		if r.balance.Sign() != 0 {
			oracle[r.key] = r.balance
		}
	}); err != nil {
		log.Fatalf("oracle scan: %v", err)
	}
	for k, v := range rebuilt {
		if v.Sign() == 0 {
			delete(rebuilt, k)
		}
	}
	liveDiff, diffs := 0, 0
	for k, v := range oracle {
		if lv, ok := listed[k]; !ok || lv.Cmp(v) != 0 {
			liveDiff++
		}
		if rv, ok := rebuilt[k]; !ok || rv.Cmp(v) != 0 {
			diffs++
		}
	}
	for k := range rebuilt {
		if _, ok := oracle[k]; !ok {
			diffs++
		}
	}
	for k, v := range listed {
		if _, ok := oracle[k]; !ok && v.Sign() != 0 {
			liveDiff++
		}
	}
	fmt.Printf("writes during listing: %d tx; live listing %s (%d rows); window (S,%d] = %d logs, %d scope accounts touched, fold %s\n", nw.Load(), tList.Round(time.Millisecond), len(listed), head, nLogs, len(firstPre), tFix.Round(time.Millisecond))
	fmt.Printf("oracle (checkpoint %d) non-zero rows: %d | raw live listing differs from S on %d rows | rewound listing differs on %d rows\n", cp, len(oracle), liveDiff, diffs)
	cpDelete(ctx, cp)
}

// cutProbe checks that S = (first log with date > cutoff) - 1 resolves with the
// per-ledger log date index.
func cutProbe(ctx context.Context) {
	ledger := fmt.Sprintf("cut-%d", time.Now().Unix())
	ensureLedger(ctx, ledger)
	_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{Ledger: ledger, Id: &commonpb.IndexID{Kind: &commonpb.IndexID_LogBuiltin{LogBuiltin: commonpb.LogBuiltinIndex_LOG_BUILTIN_INDEX_DATE}}}}}}}}})
	if err != nil {
		log.Fatalf("index: %v", err)
	}
	var dates []uint64
	for i := 0; i < 5; i++ {
		resp, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
			Ledger: ledger,
			Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{Postings: []*commonpb.Posting{{Source: "world", Destination: "a", Amount: commonpb.NewUint256FromUint64(1), Asset: "USD/2"}}, Force: true}}},
		}}}}}}})
		if err != nil {
			log.Fatalf("tx: %v", err)
		}
		dates = append(dates, resp.GetLogs()[0].GetPayload().GetApply().GetLog().GetDate().GetData())
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(time.Second)
	cutoff := (dates[2] + dates[3]) / 2 // between log 3 and log 4
	stream, err := svc.ListLogs(ctx, &servicepb.ListLogsRequest{Ledger: ledger, Options: &commonpb.ListOptions{PageSize: 1, Filter: &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_LogBuiltinUint{LogBuiltinUint: &commonpb.LogBuiltinUintCondition{Field: commonpb.LogBuiltinIndex_LOG_BUILTIN_INDEX_DATE, Cond: &commonpb.UintCondition{Min: &cutoff, MinExclusive: true}}}}}})
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	for {
		l, rerr := stream.Recv()
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				fmt.Println("ERR", rerr)
			}
			break
		}
		id := l.GetPayload().GetApply().GetLog().GetId()
		fmt.Printf("dates=%v cutoff=%d -> first log after cutoff id=%d => S=%d (expected 3); head=%d\n", dates, cutoff, id, id-1, lastLogID(ctx, ledger))
	}
}

// loadMixed writes n transactions on a ledger whose traffic is mostly not
// payments: every `every`-th transaction is a payment (kind=payment,
// payment_ref), the rest is internal noise (kind=internal). The `kind`
// transaction metadata is declared and indexed, so ListTransactions can filter
// on it server-side while ListLogs cannot.
func loadMixed(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("load-mixed", flag.ExitOnError)
	ledger := fs.String("ledger", "mixed", "")
	n := fs.Uint64("n", 1_000_000, "")
	every := fs.Uint64("every", 10, "one payment every N transactions")
	batch := fs.Int("batch", 500, "")
	workers := fs.Int("workers", 16, "")
	_ = fs.Parse(args)

	_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{
		Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateLedger{CreateLedger: &servicepb.CreateLedgerRequest{
			Name: *ledger, DefaultEnforcementMode: commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
			InitialSchema: []*commonpb.SetMetadataFieldTypeCommand{
				{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: "kind", Type: commonpb.MetadataType_METADATA_TYPE_STRING},
				{TargetType: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: "payment_ref", Type: commonpb.MetadataType_METADATA_TYPE_STRING},
			},
		}}}},
	}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create ledger: %v", err)
	}
	_, err = svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{
		Ledger: *ledger, Id: &commonpb.IndexID{Kind: &commonpb.IndexID_Metadata{Metadata: &commonpb.MetadataIndexID{Target: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: "kind"}}},
	}}}}}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		log.Fatalf("create index: %v", err)
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
				reqs := make([]*servicepb.Request, 0, len(ids))
				for _, i := range ids {
					p := &commonpb.Posting{Source: "world", Destination: fmt.Sprintf("internal:%d", i%1000), Amount: commonpb.NewUint256FromUint64(amount(i)), Asset: "USD/2"}
					md := map[string]string{"kind": "internal"}
					if i%*every == 0 {
						p.Destination = "psp:payment:" + id(i)
						md = map[string]string{"kind": "payment", "payment_ref": id(i)}
					}
					reqs = append(reqs, &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
						Ledger: *ledger,
						Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
							Postings: []*commonpb.Posting{p}, Force: true, Metadata: commonpb.MetadataFromMap(md),
						}}},
					}}})
				}
				if _, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: reqs}}}); err != nil {
					log.Fatalf("apply: %v", err)
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
	fmt.Printf("loaded %d tx on %s (1 payment every %d) in %s\n", done.Load(), *ledger, *every, time.Since(t0).Round(time.Millisecond))
}

// txsCmd reads the transactions with id in (from, to], optionally restricted
// server-side to metadata kind == -kind, split into -ranges disjoint id ranges
// read concurrently. It is the ListTransactions counterpart of `logs`.
func txsCmd(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("txs", flag.ExitOnError)
	ledger := fs.String("ledger", "mixed", "")
	from := fs.Uint64("from", 0, "exclusive lower transaction id")
	to := fs.Uint64("to", 0, "inclusive upper transaction id")
	kind := fs.String("kind", "", "metadata kind to filter on server-side (empty = no filter)")
	exists := fs.String("exists", "", "metadata key that must be present, filtered server-side (e.g. payment_ref)")
	index := fs.Bool("index", false, "create the metadata index for -exists first")
	ranges := fs.Uint64("ranges", 1, "")
	page := fs.Uint("page", 1000, "")
	_ = fs.Parse(args)
	if *to <= *from {
		log.Fatal("txs: -to must be greater than -from")
	}
	if *index && *exists != "" {
		_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{
			Ledger: *ledger, Id: &commonpb.IndexID{Kind: &commonpb.IndexID_Metadata{Metadata: &commonpb.MetadataIndexID{Target: commonpb.TargetType_TARGET_TYPE_TRANSACTION, Key: *exists}}},
		}}}}}}})
		if err != nil && status.Code(err) != codes.AlreadyExists {
			log.Fatalf("create index: %v", err)
		}
	}
	span := (*to - *from + *ranges - 1) / *ranges
	var total, withPCV atomic.Uint64
	var wg sync.WaitGroup
	t0 := time.Now()
	for r := uint64(0); r < *ranges; r++ {
		lo := *from + r*span
		hi := min(lo+span, *to)
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi uint64) {
			defer wg.Done()
			idRange := &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_BuiltinUint{BuiltinUint: &commonpb.BuiltinUintCondition{
				Field: commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ID,
				Cond:  &commonpb.UintCondition{Min: &lo, MinExclusive: true, Max: &hi},
			}}}
			filter := idRange
			if *exists != "" {
				filter = &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: []*commonpb.QueryFilter{
					idRange,
					{Filter: &commonpb.QueryFilter_Field{Field: &commonpb.FieldCondition{
						Field:     &commonpb.FieldRef{Metadata: *exists},
						Condition: &commonpb.FieldCondition_ExistsCond{ExistsCond: &commonpb.ExistsCondition{}},
					}}},
				}}}}
			}
			if *kind != "" {
				filter = &commonpb.QueryFilter{Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: []*commonpb.QueryFilter{
					idRange,
					{Filter: &commonpb.QueryFilter_Field{Field: &commonpb.FieldCondition{
						Field:     &commonpb.FieldRef{Metadata: "kind"},
						Condition: &commonpb.FieldCondition_StringCond{StringCond: &commonpb.StringCondition{Value: &commonpb.StringCondition_Hardcoded{Hardcoded: *kind}}},
					}}},
				}}}}
			}
			var cursor string
			for {
				stream, err := svc.ListTransactions(ctx, &servicepb.ListTransactionsRequest{Ledger: *ledger, Options: &commonpb.ListOptions{PageSize: uint32(*page), Cursor: cursor, Filter: filter}})
				if err != nil {
					log.Fatalf("list transactions: %v", err)
				}
				for {
					tx, rerr := stream.Recv()
					if errors.Is(rerr, io.EOF) {
						break
					}
					if rerr != nil {
						log.Fatalf("recv: %v", rerr)
					}
					total.Add(1)
					if len(tx.GetPostCommitVolumes().GetVolumesByAccount()) > 0 {
						withPCV.Add(1)
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
		}(lo, hi)
	}
	wg.Wait()
	el := time.Since(t0)
	fmt.Printf("txs %s ids (%d,%d] kind=%q exists=%q ranges=%d: %d transactions (%d with post_commit_volumes) in %s (%.0f tx/s returned, %.0f ids/s covered)\n",
		*ledger, *from, *to, *kind, *exists, *ranges, total.Load(), withPCV.Load(), el.Round(time.Millisecond), float64(total.Load())/el.Seconds(), float64(*to-*from)/el.Seconds())
}

// retag overwrites the `kind` metadata of one existing transaction: transaction
// metadata is mutable, so a filtered ListTransactions re-read of a past window
// can return a different set than the first read, while its logs do not change.
func retag(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("retag", flag.ExitOnError)
	ledger := fs.String("ledger", "mixed", "")
	txID := fs.Uint64("tx", 10, "")
	kind := fs.String("kind", "internal", "")
	_ = fs.Parse(args)
	_, err := svc.Apply(ctx, &servicepb.ApplyRequest{Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: &servicepb.ApplyBatch{Requests: []*servicepb.Request{{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: *ledger,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_AddMetadata{AddMetadata: &commonpb.SaveMetadataCommand{
			Target:   &commonpb.Target{Target: &commonpb.Target_TransactionId{TransactionId: *txID}},
			Metadata: commonpb.MetadataFromMap(map[string]string{"kind": *kind}),
		}}},
	}}}}}}})
	if err != nil {
		log.Fatalf("retag: %v", err)
	}
	fmt.Printf("transaction %d on %s now has kind=%q\n", *txID, *ledger, *kind)
}
