package crud

import (
	"encoding/json"
	"strings"
	"testing"
)

func body(t *testing.T, s string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var allowed = map[string]string{"name": "text", "n": "int", "d": "date"}

func TestBuildUpdateUsesPlaceholdersAndCasts(t *testing.T) {
	q := &Q{}
	q.And("id = " + q.Arg("X") + "::uuid")
	sql, err := BuildUpdate("t", "id", allowed, body(t, `{"name":"a'; DROP TABLE t;--","n":5}`), q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "DROP") || !strings.Contains(sql, "n = $2::int") || !strings.Contains(sql, "name = $3::text") {
		t.Fatalf("%s", sql)
	}
	if len(q.Args) != 3 || q.Args[2] != "a'; DROP TABLE t;--" || q.Args[1] != "5" {
		t.Fatalf("%v", q.Args)
	}
}

func TestBuildUpdateRejectsUnknownAndEmpty(t *testing.T) {
	if _, err := BuildUpdate("t", "id", allowed, body(t, `{"tenant_id":"x"}`), &Q{}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := BuildUpdate("t", "id", allowed, body(t, `{}`), &Q{}); err == nil {
		t.Fatal("empty patch accepted")
	}
}

func TestBuildUpdateNullSetsNull(t *testing.T) {
	q := &Q{}
	if _, err := BuildUpdate("t", "id", allowed, body(t, `{"name":null}`), q); err != nil {
		t.Fatal(err)
	}
	if q.Args[0] != nil {
		t.Fatalf("%v", q.Args)
	}
}

func TestValueConversions(t *testing.T) {
	for in, want := range map[string]any{`"s"`: "s", `12`: "12", `true`: "true", `null`: nil, `{"a":1}`: `{"a":1}`} {
		got, err := Value(json.RawMessage(in))
		if err != nil || got != want {
			t.Errorf("%s: got %v (%v) want %v", in, got, err, want)
		}
	}
	if _, err := Value(json.RawMessage(`{bad`)); err == nil {
		t.Fatal("bad json accepted")
	}
}

func TestWhereBuilder(t *testing.T) {
	q := &Q{}
	if q.Where() != "" {
		t.Fatal("empty where")
	}
	q.And("a = " + q.Arg(1))
	q.And("b = " + q.Arg(2))
	if q.Where() != " WHERE a = $1 AND b = $2" {
		t.Fatal(q.Where())
	}
}
