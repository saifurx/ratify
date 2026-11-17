// Package pagination implements opaque, signed keyset cursors. A cursor
// encodes only (created_at, id). The tenant is bound into the signature but
// NOT encoded, so a cursor issued for tenant A is rejected for tenant B.
package pagination

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Position struct {
	CreatedAt time.Time
	ID        string
}

var ErrBadCursor = errors.New("invalid cursor")

type Codec struct{ secret []byte }

func NewCodec(secret string) Codec { return Codec{secret: []byte(secret)} }

type wire struct {
	T string `json:"t"`
	I string `json:"i"`
}

func (c Codec) mac(tenantID string, payload []byte) []byte {
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte(tenantID))
	m.Write([]byte{0})
	m.Write(payload)
	return m.Sum(nil)
}

func (c Codec) Encode(tenantID string, p Position) string {
	payload, _ := json.Marshal(wire{T: p.CreatedAt.UTC().Format(time.RFC3339Nano), I: p.ID})
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(c.mac(tenantID, payload))
}

func (c Codec) Decode(tenantID, s string) (Position, error) {
	enc := base64.RawURLEncoding
	a, b, ok := strings.Cut(s, ".")
	if !ok {
		return Position{}, ErrBadCursor
	}
	payload, err1 := enc.DecodeString(a)
	sig, err2 := enc.DecodeString(b)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, c.mac(tenantID, payload)) {
		return Position{}, ErrBadCursor
	}
	var w wire
	if json.Unmarshal(payload, &w) != nil {
		return Position{}, ErrBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, w.T)
	if err != nil || w.I == "" {
		return Position{}, ErrBadCursor
	}
	return Position{CreatedAt: t, ID: w.I}, nil
}
