package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The result-file rows of lettering/1 (docs/technical/transaction-level-results.md §6), with
// their fields in the order the results doc lists them: encoding/json writes struct fields in
// declaration order, as the JSON Schema order requires.
type pspItem struct {
	Tx         uint64 `json:"tx"`
	State      string `json:"state"`
	Amount     string `json:"amount,omitempty"`
	HoldAmount string `json:"holdAmount,omitempty"`
	InsertedAt string `json:"insertedAt"`
}

type productItem struct {
	Tx         uint64 `json:"tx"`
	BusinessID string `json:"businessId"`
	HoldID     string `json:"holdId"`
	Amount     string `json:"amount"`
	InsertedAt string `json:"insertedAt"`
}

type resultFlowRow struct {
	Ref           string        `json:"ref"`
	Asset         string        `json:"asset"`
	Class         string        `json:"class"`
	Outcome       string        `json:"outcome"`
	PspAmount     string        `json:"pspAmount"`
	ProductAmount string        `json:"productAmount"`
	Drift         string        `json:"drift"`
	Impact        *string       `json:"impact,omitempty"`
	FirstSeen     string        `json:"firstSeen,omitempty"`
	FirstSide     string        `json:"firstSide,omitempty"`
	BreakOn       string        `json:"breakOn,omitempty"`
	Psp           []pspItem     `json:"psp"`
	Product       []productItem `json:"product"`
}

type resultStockRow struct {
	Side      string `json:"side"`
	Hold      string `json:"hold"`
	Asset     string `json:"asset"`
	Prefix    string `json:"prefix"`
	HoldID    string `json:"holdId"`
	OpenSign  string `json:"openSign"`
	Balance   string `json:"balance"`
	Class     string `json:"class"`
	Outcome   string `json:"outcome"`
	Lifecycle string `json:"lifecycle"`
	OpenedAt  string `json:"openedAt"`
	AgeDays   int    `json:"ageDays"`
	Bucket    string `json:"bucket"`
}

type resultBreakRow struct {
	BreakID   string `json:"breakId"`
	Leg       string `json:"leg"`
	Priority  int    `json:"priority"`
	Lifecycle string `json:"lifecycle"`
	OpenedOn  string `json:"openedOn"`
	Amount    string `json:"amount"`
	resultFlowRow
}

type unclassifiedRow struct {
	Side       string `json:"side"`
	Tx         uint64 `json:"tx"`
	Ref        string `json:"ref"`
	Asset      string `json:"asset"`
	Outcome    string `json:"outcome"`
	State      string `json:"state"`
	Amount     string `json:"amount"`
	InsertedAt string `json:"insertedAt"`
}

// fileSize measures the result files of one day (EN-2322: "measure the self-contained rows
// before the format is frozen, the flow file first"). It books nothing on a ledger: it
// generates a day of -payments payments with a realistic mix of classes, writes each file as
// gzipped NDJSON with a fixed level and no name or timestamp (results doc §8), and reports its
// size, the time to encode and compress it, and the time to hash it.
func fileSize(args []string) {
	fs := flag.NewFlagSet("file-size", flag.ExitOnError)
	payments := fs.Int("payments", 1_000_000, "payments in the day's flow")
	refLen := fs.Int("ref-len", 27, "length of a payment reference (27: a Stripe id; ~110: a base64 Payments id)")
	openHolds := fs.Int("open-holds", 60_000, "open holds in the stock file")
	level := fs.Int("level", gzip.DefaultCompression, "gzip level")
	out := fs.String("out", "", "directory to write the files to (empty: in memory only)")
	_ = fs.Parse(args)
	rng := rand.New(rand.NewPCG(1, 2))

	ref := func(i int) string {
		b := make([]byte, (*refLen*3+3)/4)
		for j := range b {
			b[j] = byte(rng.IntN(256))
		}
		s := base64.RawURLEncoding.EncodeToString(b)
		if len(s) > *refLen {
			s = s[:*refLen]
		}
		return s
	}
	at := func(sec int) string {
		return time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Add(time.Duration(sec) * time.Second).Format(time.RFC3339)
	}
	str := func(v int64) string { return strconv.FormatInt(v, 10) }

	// The day's mix: most payments matched, a few percent carried or broken.
	type share struct {
		class, outcome string
		p              float64
	}
	mix := []share{{"matched", "ok", 0.88}, {"in_progress", "ok", 0.04}, {"unapplied_payment", "pending", 0.03},
		{"applied_before_final", "pending", 0.02}, {"unapplied_payment", "break", 0.01}, {"under_applied", "break", 0.005},
		{"over_applied", "break", 0.005}, {"failed", "ok", 0.01}}
	var flow, carried []resultFlowRow
	var brk []resultBreakRow
	pspTx, prodTx := uint64(1_200_000), uint64(880_000)
	for i := 0; i < *payments; i++ {
		x := rng.Float64()
		c := mix[len(mix)-1]
		for _, m := range mix {
			if x < m.p {
				c = m
				break
			}
			x -= m.p
		}
		amt := int64(100 + rng.IntN(500_000))
		t := rng.IntN(86_400)
		inv := fmt.Sprintf("INV-2026-%07d", rng.IntN(10_000_000))
		r := resultFlowRow{Ref: ref(i), Asset: "EUR/2", Class: c.class, Outcome: c.outcome, FirstSeen: "2026-09-24", FirstSide: "psp"}
		pending := pspItem{Tx: pspTx, State: "payin.pending", HoldAmount: str(amt), InsertedAt: at(t)}
		final := pspItem{Tx: pspTx + 1, State: "payin.succeeded", Amount: str(amt), InsertedAt: at(t + 120)}
		app := func(a int64) productItem {
			return productItem{Tx: prodTx, BusinessID: inv, HoldID: inv, Amount: str(a), InsertedAt: at(t + 300)}
		}
		pspTx += 2
		prodTx++
		var psp, prod int64
		switch c.class {
		case "matched":
			r.Psp, r.Product, psp, prod = []pspItem{pending, final}, []productItem{app(amt)}, amt, amt
		case "in_progress":
			r.Psp, r.FirstSeen, r.FirstSide = []pspItem{pending}, "", ""
		case "unapplied_payment":
			r.Psp, psp, r.BreakOn = []pspItem{pending, final}, amt, "2026-09-25"
		case "applied_before_final":
			r.Psp, r.Product, prod, r.FirstSide, r.BreakOn = []pspItem{pending}, []productItem{app(amt)}, amt, "product", "2026-10-01"
		case "under_applied":
			r.Psp, r.Product, psp, prod = []pspItem{pending, final}, []productItem{app(amt - 500)}, amt, amt-500
		case "over_applied":
			r.Psp, r.Product, psp, prod = []pspItem{pending, final}, []productItem{app(amt + 500)}, amt, amt+500
		case "failed":
			r.Psp = []pspItem{pending, {Tx: pspTx, State: "payin.compensate", HoldAmount: str(amt), InsertedAt: at(t + 600)}}
			pspTx++
		}
		if r.Product == nil {
			r.Product = []productItem{}
		}
		r.PspAmount, r.ProductAmount, r.Drift = str(psp), str(prod), str(psp-prod)
		imp := str(psp - prod)
		r.Impact = &imp
		flow = append(flow, r)
		if psp != prod {
			cr := r
			cr.Impact = nil
			carried = append(carried, cr)
		}
		if c.outcome == "break" {
			brk = append(brk, resultBreakRow{BreakID: fmt.Sprintf("%016x", rng.Uint64()), Leg: "flow", Priority: 2, Lifecycle: "new",
				OpenedOn: "2026-09-24", Amount: str(psp - prod), resultFlowRow: r})
		}
	}
	var stock []resultStockRow
	for i := 0; i < *openHolds; i++ {
		id := fmt.Sprintf("INV-2026-%07d", rng.IntN(10_000_000))
		age := rng.IntN(60)
		stock = append(stock, resultStockRow{Side: "product", Hold: "main:hold:invoice:" + id, Asset: "EUR/2", Prefix: "main:hold:invoice:",
			HoldID: id, OpenSign: "negative", Balance: str(-int64(100 + rng.IntN(500_000))), Class: "open", Outcome: "ok",
			Lifecycle: "persisting", OpenedAt: at(-age * 86_400), AgeDays: age, Bucket: "8-30d"})
	}
	var uncl []unclassifiedRow
	for i := 0; i < *payments/1000; i++ {
		uncl = append(uncl, unclassifiedRow{Side: "psp", Tx: pspTx + uint64(i), Ref: ref(i), Asset: "EUR/2", Outcome: "warning",
			State: "payin.refunded", Amount: str(int64(100 + rng.IntN(50_000))), InsertedAt: at(rng.IntN(86_400))})
	}

	write := func(name string, rows int, each func(enc *json.Encoder)) {
		t0 := time.Now()
		var raw countWriter
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, *level)
		if err != nil {
			log.Fatal(err)
		}
		enc := json.NewEncoder(teeWriter{zw, &raw})
		each(enc)
		if err := zw.Close(); err != nil {
			log.Fatal(err)
		}
		encT := time.Since(t0)
		t1 := time.Now()
		sum := sha256.Sum256(buf.Bytes())
		shaT := time.Since(t1)
		if *out != "" {
			if err := os.MkdirAll(*out, 0o755); err != nil {
				log.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(*out, name), buf.Bytes(), 0o644); err != nil {
				log.Fatal(err)
			}
		}
		per := 0.0
		if rows > 0 {
			per = float64(buf.Len()) / float64(rows)
		}
		fmt.Printf("  %-24s %9d rows  %8.1f MB raw  %7.1f MB gz  %6.1f B/row gz  encode+gzip %6s  sha256 %5s  %x…\n",
			name, rows, float64(raw)/1e6, float64(buf.Len())/1e6, per, encT.Round(time.Millisecond), shaT.Round(time.Millisecond), sum[:4])
	}
	fmt.Printf("day of %d payments, refs of %d chars, %d open holds, gzip level %d\n", *payments, *refLen, *openHolds, *level)
	write("flow.ndjson.gz", len(flow), func(e *json.Encoder) {
		for i := range flow {
			_ = e.Encode(&flow[i])
		}
	})
	write("carried.ndjson.gz", len(carried), func(e *json.Encoder) {
		for i := range carried {
			_ = e.Encode(&carried[i])
		}
	})
	write("stock.ndjson.gz", len(stock), func(e *json.Encoder) {
		for i := range stock {
			_ = e.Encode(&stock[i])
		}
	})
	write("breaks.ndjson.gz", len(brk), func(e *json.Encoder) {
		for i := range brk {
			_ = e.Encode(&brk[i])
		}
	})
	write("unclassified.ndjson.gz", len(uncl), func(e *json.Encoder) {
		for i := range uncl {
			_ = e.Encode(&uncl[i])
		}
	})
}

type countWriter int64

func (c *countWriter) Write(p []byte) (int, error) { *c += countWriter(len(p)); return len(p), nil }

type teeWriter struct {
	a *gzip.Writer
	b *countWriter
}

func (t teeWriter) Write(p []byte) (int, error) {
	_, _ = t.b.Write(p)
	return t.a.Write(p)
}
