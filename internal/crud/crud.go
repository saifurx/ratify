// Package crud holds small SQL-building helpers shared by the entity
// modules. Nothing here ever adds a tenant predicate: RLS supplies it.
package crud

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"ratify/internal/db"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

// Q accumulates WHERE conditions and positional arguments.
type Q struct {
	conds []string
	Args  []any
}

func (q *Q) Arg(v any) string { q.Args = append(q.Args, v); return fmt.Sprintf("$%d", len(q.Args)) }
func (q *Q) And(cond string)  { q.conds = append(q.conds, cond) }
func (q *Q) Where() string {
	if len(q.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(q.conds, " AND ")
}

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// List runs a keyset-paginated query ordered by (created_at DESC, id DESC).
// sel is "SELECT cols FROM table". It returns up to limit items and, if more
// exist, the position of the last returned item.
func List[T any](ctx context.Context, tx db.Tx, sel string, q *Q, after *pagination.Position, limit int,
	scan func(pgx.Row) (T, error), pos func(T) pagination.Position) ([]T, *pagination.Position, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	if after != nil {
		q.And(fmt.Sprintf("(created_at, id) < (%s::timestamptz, %s::uuid)", q.Arg(after.CreatedAt), q.Arg(after.ID)))
	}
	sql := sel + q.Where() + fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT %d", limit+1)
	rows, err := tx.Query(ctx, sql, q.Args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(items) > limit {
		items = items[:limit]
		p := pos(items[limit-1])
		return items, &p, nil
	}
	return items, nil, nil
}

// BuildUpdate builds "UPDATE table SET ... WHERE <q conditions> RETURNING cols"
// from a JSON patch body. allowed maps column -> SQL type; unknown fields are
// rejected. JSON null sets the column to NULL. The caller has already added
// the id and scope conditions to q.
func BuildUpdate(table, returning string, allowed map[string]string, body map[string]json.RawMessage, q *Q) (string, error) {
	if len(body) == 0 {
		return "", platform.Invalid{Msg: "no fields to update"}
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sets := make([]string, 0, len(keys))
	for _, k := range keys {
		typ, ok := allowed[k]
		if !ok {
			return "", platform.Invalid{Msg: "field not updatable: " + k}
		}
		v, err := Value(body[k])
		if err != nil {
			return "", platform.Invalid{Msg: "invalid value for " + k}
		}
		sets = append(sets, fmt.Sprintf("%s = %s::%s", k, q.Arg(v), typ))
	}
	return fmt.Sprintf("UPDATE %s SET %s%s RETURNING %s", table, strings.Join(sets, ", "), q.Where(), returning), nil
}

// Value converts a JSON scalar to a text-safe query argument (nil for null).
func Value(raw json.RawMessage) (any, error) {
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case nil:
		return nil, nil
	case json.Number:
		return x.String(), nil
	case string:
		return x, nil
	case bool:
		return fmt.Sprint(x), nil
	default:
		b, _ := json.Marshal(x) // objects/arrays (jsonb columns)
		return string(b), nil
	}
}
