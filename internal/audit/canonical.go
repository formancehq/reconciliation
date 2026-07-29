// Package audit owns the module's tamper-evident journal: the canonical
// encoding of what gets hashed, the keyed hash chain itself, and the asymmetric
// signatures that make a period seal verifiable by someone who does not trust
// this service.
//
// One rule governs this package, and it is the expensive lesson of the Ledger
// V2 log: the canonical encoding has exactly ONE implementation. V2 computed
// its log hash in a PL/pgSQL trigger that reproduced Go's json.Marshal output
// by hand — down to placeholder fields — and the two drifted, which is why
// there is a migration in that repo called "Fix hashing function". Here the
// encoding lives in Go, runs inside the business transaction, and the database
// is used only to FORBID rewriting (revoked privileges plus a trigger that
// raises), never to compute anything.
package audit

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
)

// Canonicalize rewrites arbitrary JSON into the module's canonical form so that
// two semantically identical payloads always produce identical bytes — the
// property a hash needs and that jsonb cannot provide, since it discards key
// order and rewrites number literals.
//
// The form is JCS-inspired (RFC 8785) and its rules are deliberately few, so a
// client in another language can reimplement them to re-verify an entry:
//
//  1. Object keys are sorted by their UTF-8 byte sequence.
//  2. No insignificant whitespace: no space after ':' or ','.
//  3. Number literals are preserved exactly as they appeared in the input —
//     never re-parsed through a float. Amounts in reconciliation evidence are
//     decimal strings, so this rule costs nothing and removes the precision
//     hazard that usually breaks this kind of scheme.
//  4. Strings use standard JSON escaping with HTML escaping disabled, so the
//     output matches what a non-Go implementation would emit.
//
// Duplicate keys in the input are resolved by last-one-wins during decoding,
// which is Go's behaviour; the canonical output then contains the key once.
func Canonicalize(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("null"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonicalize: decoding input: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("canonicalize: trailing content after top-level JSON value")
	}

	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalizeValue marshals a Go value and canonicalizes the result. Used for
// the memento payloads this package builds itself, where the input is a struct
// rather than bytes off the wire.
func CanonicalizeValue(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: marshalling value: %w", err)
	}
	return Canonicalize(raw)
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		// Rule 3: emit the literal verbatim.
		buf.WriteString(t.String())
	case string:
		return writeCanonicalString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// Rule 1: byte-wise sort. Go's string comparison is byte-wise on the
		// UTF-8 representation, which is exactly what RFC 8785 requires.
		sort.Strings(keys)

		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonicalize: unsupported JSON value of type %T", v)
	}
	return nil
}

// writeCanonicalString applies rule 4. json.Encoder appends a newline, which is
// trimmed.
func writeCanonicalString(buf *bytes.Buffer, s string) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("canonicalize: encoding string: %w", err)
	}
	buf.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
	return nil
}

// payloadWriter builds the length-prefixed byte sequences that feed the chain
// hash. Every variable-length field is preceded by its length, so no
// concatenation of two different field sets can collide — the ambiguity that
// makes naive `a||b` hashing forgeable.
type payloadWriter struct {
	buf bytes.Buffer
}

func (w *payloadWriter) uint64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.buf.Write(b[:])
}

func (w *payloadWriter) uint32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.buf.Write(b[:])
}

func (w *payloadWriter) byteTag(v byte) {
	w.buf.WriteByte(v)
}

// bytesField writes a length-prefixed blob. A nil and an empty slice encode
// identically; where the distinction matters the caller writes a tag first.
func (w *payloadWriter) bytesField(v []byte) {
	w.uint32(uint32(len(v)))
	w.buf.Write(v)
}

func (w *payloadWriter) stringField(v string) {
	w.bytesField([]byte(v))
}

// stringsField writes a length-prefixed sequence of length-prefixed strings.
func (w *payloadWriter) stringsField(v []string) {
	w.uint32(uint32(len(v)))
	for _, s := range v {
		w.stringField(s)
	}
}

func (w *payloadWriter) bytes() []byte { return w.buf.Bytes() }
