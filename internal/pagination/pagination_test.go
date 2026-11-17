package pagination

import (
	"encoding/base64"
	"testing"
	"time"
)

var base64RawURL = base64.RawURLEncoding

var pos = Position{CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC), ID: "11111111-1111-1111-1111-111111111111"}

const tenantA, tenantB = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

func TestRoundTrip(t *testing.T) {
	c := NewCodec("s")
	got, err := c.Decode(tenantA, c.Encode(tenantA, pos))
	if err != nil || !got.CreatedAt.Equal(pos.CreatedAt) || got.ID != pos.ID {
		t.Fatalf("%v %v", got, err)
	}
}

func TestCursorBoundToTenant(t *testing.T) {
	c := NewCodec("s")
	if _, err := c.Decode(tenantB, c.Encode(tenantA, pos)); err == nil {
		t.Fatal("a cursor must not decode under a different tenant")
	}
}

func TestCursorEncodesNoTenantID(t *testing.T) {
	c := NewCodec("s")
	enc := c.Encode(tenantA, pos)
	for _, part := range []string{tenantA, "aaaaaaaa"} {
		if contains(enc, part) {
			t.Fatal("cursor text contains the tenant id")
		}
	}
	// decode the payload half and check it too
	payload := enc[:indexByte(enc, '.')]
	raw, _ := decodeB64(payload)
	if contains(string(raw), tenantA) {
		t.Fatal("cursor payload contains the tenant id")
	}
}

func TestTamperingAndGarbage(t *testing.T) {
	c := NewCodec("s")
	enc := c.Encode(tenantA, pos)
	for _, bad := range []string{"", "x", ".", enc + "A", "A" + enc, enc[:len(enc)-2], "a.b.c"} {
		if _, err := c.Decode(tenantA, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestDifferentSecretRejected(t *testing.T) {
	if _, err := NewCodec("other").Decode(tenantA, NewCodec("s").Encode(tenantA, pos)); err == nil {
		t.Fatal("cursor signed with another secret accepted")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
func decodeB64(s string) ([]byte, error) { return base64RawURL.DecodeString(s) }
