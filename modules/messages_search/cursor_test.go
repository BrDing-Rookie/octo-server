package messages_search

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestCursor_RoundTrip(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	encoded := encodeCursor(cfg, 1717000000, 9876543210, nil, 0)
	if encoded == "" {
		t.Fatalf("encoded cursor is empty")
	}
	ts, msgID, score, subSeq, err := decodeCursor(cfg, encoded)
	if err != nil {
		t.Fatalf("decodeCursor unexpected error: %v", err)
	}
	if ts != 1717000000 || msgID != 9876543210 {
		t.Fatalf("decoded mismatch: ts=%d msgID=%d", ts, msgID)
	}
	if score != nil {
		t.Fatalf("legacy 2-tuple cursor should decode score=nil, got %v", *score)
	}
	if subSeq != 0 {
		t.Fatalf("plain-doc cursor must decode subSeq=0, got %d", subSeq)
	}
}

func TestCursor_RelevanceRoundTrip(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	want := 12.345
	encoded := encodeCursor(cfg, 1717000000, 9876543210, &want, 0)
	if encoded == "" {
		t.Fatalf("encoded relevance cursor is empty")
	}
	ts, msgID, score, subSeq, err := decodeCursor(cfg, encoded)
	if err != nil {
		t.Fatalf("decodeCursor unexpected error: %v", err)
	}
	if ts != 1717000000 || msgID != 9876543210 {
		t.Fatalf("decoded mismatch: ts=%d msgID=%d", ts, msgID)
	}
	if score == nil {
		t.Fatalf("relevance cursor should decode score non-nil")
	}
	if *score != want {
		t.Fatalf("score mismatch: got %v want %v", *score, want)
	}
	if subSeq != 0 {
		t.Fatalf("plain-doc cursor must decode subSeq=0, got %d", subSeq)
	}
}

func TestCursor_LegacyFormatBackCompat(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	enc := encodeCursor(cfg, 1, 2, nil, 0)
	_, _, score, _, err := decodeCursor(cfg, enc)
	if err != nil {
		t.Fatalf("decode legacy: %v", err)
	}
	if score != nil {
		t.Fatalf("legacy cursor must decode score==nil, got %v", *score)
	}
}

func TestCursor_TamperRejected(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	enc := encodeCursor(cfg, 1717000000, 100, nil, 0)
	tampered := enc[:len(enc)-2] + "AA"
	if _, _, _, _, err := decodeCursor(cfg, tampered); err == nil {
		t.Fatalf("expected tamper error, got nil")
	}
}

func TestCursor_DifferentKeyRejected(t *testing.T) {
	enc := encodeCursor(SearchConfig{CursorHMAC: "key-a"}, 1, 2, nil, 0)
	if _, _, _, _, err := decodeCursor(SearchConfig{CursorHMAC: "key-b"}, enc); err == nil {
		t.Fatalf("expected sig mismatch, got nil")
	}
}

func TestCursor_EmptyAndMalformed(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	if _, _, _, _, err := decodeCursor(cfg, ""); err == nil {
		t.Fatalf("expected empty cursor error")
	}
	if _, _, _, _, err := decodeCursor(cfg, "@@@@notbase64"); err == nil {
		t.Fatalf("expected malformed cursor error")
	}
	if _, _, _, _, err := decodeCursor(cfg, "AAAA"); err == nil {
		t.Fatalf("expected too-short cursor error")
	}
}

// TestCursor_SubSeqRoundTrip pins the Part B tiebreaker: encode subSeq=N,
// decode back yields N. Both time_* and relevance shapes covered.
func TestCursor_SubSeqRoundTrip(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	enc := encodeCursor(cfg, 1717000000, 42, nil, 3)
	_, _, _, subSeq, err := decodeCursor(cfg, enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if subSeq != 3 {
		t.Fatalf("subSeq round-trip: got %d want 3", subSeq)
	}

	relScore := 5.5
	encRel := encodeCursor(cfg, 1717000000, 42, &relScore, 7)
	_, _, _, subSeqRel, err := decodeCursor(cfg, encRel)
	if err != nil {
		t.Fatalf("decode rel: %v", err)
	}
	if subSeqRel != 7 {
		t.Fatalf("relevance subSeq round-trip: got %d want 7", subSeqRel)
	}
}

// TestCursor_PreSubSeqLegacyDecodesZero — a cursor written before the
// SubSeq field existed (no "q" key in the payload) must decode cleanly with
// subSeq=0. This is the platform-side smooth-degrade contract: old clients
// holding pre-Part-B cursors keep working without re-issue.
func TestCursor_PreSubSeqLegacyDecodesZero(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	body := []byte(`{"ts":1717000000,"id":42}`)
	enc := signCursorBody(cfg, body)
	ts, msgID, score, subSeq, err := decodeCursor(cfg, enc)
	if err != nil {
		t.Fatalf("legacy cursor must decode: %v", err)
	}
	if ts != 1717000000 || msgID != 42 {
		t.Fatalf("legacy decode: ts=%d id=%d", ts, msgID)
	}
	if score != nil {
		t.Fatalf("legacy time_* cursor must decode score==nil")
	}
	if subSeq != 0 {
		t.Fatalf("legacy cursor must decode subSeq=0, got %d", subSeq)
	}
}

// TestCursor_PreSubSeqLegacyAsSearchAfter — the legacy-cursor decode path
// must feed search_after with subSeq=0 in the trailing slot. Under OS's
// exclusive search_after, (ts, msgID, 0) lets every same-(ts,msgID) virtual
// child (subSeq>=1) surface on the next page rather than being silently
// skipped.
func TestCursor_PreSubSeqLegacyAsSearchAfter(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	body := []byte(`{"ts":1717000000,"id":42}`)
	enc := signCursorBody(cfg, body)

	sa, ok := decodeCursorAsSearchAfter(cfg, enc, false)
	if !ok {
		t.Fatalf("legacy cursor must decode for search_after")
	}
	if len(sa) != 3 {
		t.Fatalf("time_* search_after must be 3-tuple [ts, msgID, subSeq]; got %v", sa)
	}
	if got, ok := sa[2].(int); !ok || got != 0 {
		t.Fatalf("legacy search_after[2] must be int(0); got %T(%v)", sa[2], sa[2])
	}
}

// TestCursor_SubSeqOmitemptyWhenZero — pins the wire-byte contract: a
// cursor with subSeq=0 must NOT include a "q" key in its body. This keeps
// pre-Part-B cursors and post-Part-B/subSeq=0 cursors byte-identical so
// already-issued cursors decode unchanged.
func TestCursor_SubSeqOmitemptyWhenZero(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	enc := encodeCursor(cfg, 1, 2, nil, 0)
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("decode b64: %v", err)
	}
	body := raw[:len(raw)-cursorSigLen]
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if _, present := m["q"]; present {
		t.Fatalf("subSeq=0 must omit \"q\" key on the wire; got body=%s", string(body))
	}
	if !strings.Contains(string(body), `"ts"`) || !strings.Contains(string(body), `"id"`) {
		t.Fatalf("body missing required keys: %s", string(body))
	}
}

// signCursorBody mirrors encodeCursor's HMAC-and-base64 step but accepts an
// arbitrary JSON body — used to construct legacy-shape cursors (no "q"
// field) for the smooth-degrade tests above.
func signCursorBody(cfg SearchConfig, body []byte) string {
	mac := hmac.New(sha256.New, hmacKeyFn(cfg))
	mac.Write(body)
	sig := mac.Sum(nil)[:cursorSigLen]
	return base64.RawURLEncoding.EncodeToString(append(append([]byte{}, body...), sig...))
}
