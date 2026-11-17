package crud

import (
	"encoding/json"
	"net/http"
	"strconv"

	"ratify/internal/pagination"
	"ratify/internal/platform"
	"ratify/internal/tenant"
)

// PathID returns the {name} path value if it is a UUID, else ErrNotFound
// (an id that cannot exist is a 404, not a 500).
func PathID(r *http.Request, name string) (string, error) {
	id := r.PathValue(name)
	if !tenant.Valid(id) {
		return "", platform.ErrNotFound
	}
	return id, nil
}

// DecodeBody reads a JSON object body (size-limited) as raw fields.
func DecodeBody(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var m map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil || m == nil {
		return nil, platform.Invalid{Msg: "body must be a JSON object"}
	}
	return m, nil
}

// ParsePage reads limit and cursor. A cursor that was not issued to this
// tenant (or was tampered with) is rejected: nothing is returned.
func ParsePage(r *http.Request, c pagination.Codec) (int, *pagination.Position, error) {
	limit := DefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxLimit {
			return 0, nil, platform.Invalid{Msg: "limit must be 1-200"}
		}
		limit = n
	}
	cur := r.URL.Query().Get("cursor")
	if cur == "" {
		return limit, nil, nil
	}
	tid, err := tenant.From(r.Context())
	if err != nil {
		return 0, nil, err
	}
	p, err := c.Decode(tid, cur)
	if err != nil {
		return 0, nil, platform.Invalid{Msg: "invalid cursor"}
	}
	return limit, &p, nil
}

// WriteList writes {"items": [...], "next_cursor": "..."|null}.
func WriteList(w http.ResponseWriter, r *http.Request, c pagination.Codec, items any, next *pagination.Position) {
	out := map[string]any{"items": items, "next_cursor": nil}
	if next != nil {
		tid, _ := tenant.From(r.Context())
		out["next_cursor"] = c.Encode(tid, *next)
	}
	platform.WriteJSON(w, http.StatusOK, out)
}

// OptionalUUID validates an optional uuid query filter.
func OptionalUUID(r *http.Request, name string) (string, error) {
	v := r.URL.Query().Get(name)
	if v != "" && !tenant.Valid(v) {
		return "", platform.Invalid{Msg: name + " must be a UUID"}
	}
	return v, nil
}

func Str(m map[string]json.RawMessage, k string) (string, bool) {
	raw, ok := m[k]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}
