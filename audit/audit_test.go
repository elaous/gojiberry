package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEventHasNoFreeTextField(t *testing.T) {
	closed := map[reflect.Type]bool{
		reflect.TypeOf(Kind("")): true, reflect.TypeOf(Outcome("")): true, reflect.TypeOf(Actor("")): true,
		reflect.TypeOf(Reason("")): true, reflect.TypeOf(Ref("")): true,
	}
	rt := reflect.TypeOf(Event{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		switch f.Type.Kind() {
		case reflect.String:
			if !closed[f.Type] {
				t.Errorf("field %s is a free string", f.Name)
			}
		case reflect.Int, reflect.Struct: // Count, Time
		default:
			t.Errorf("field %s has type %s", f.Name, f.Type)
		}
	}
}

func TestLogWritesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)
	l.now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 999, time.FixedZone("x", 3600)) }
	if err := l.Log(Event{Kind: BackupRestored, Outcome: OK, Actor: Operator, Count: 12}); err != nil {
		t.Fatal(err)
	}
	want := `{"time":"2026-10-11T11:00:00Z","kind":"backup.restored","outcome":"ok","actor":"operator","count":12}` + "\n"
	if buf.String() != want {
		t.Fatalf("%q", buf.String())
	}
	var back Event
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil || !back.Valid() {
		t.Fatalf("it reads back valid: %v", err)
	}
}

func TestOffAllowlistIsRefusedAndCounted(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)
	bad := []Event{
		{Kind: "ana@example.com created a doc", Outcome: OK, Actor: Owner},
		{Kind: KeyEnrolled, Outcome: "fine", Actor: Owner},
		{Kind: KeyEnrolled, Outcome: OK, Actor: "ana"},
		{Kind: SignInFailed, Outcome: Denied, Actor: Anonymous, Reason: "wrong password for ana"},
		{Kind: SignInFailed, Outcome: Denied, Actor: Anonymous, Ref: "ana@example.com"},
		{Kind: SignInFailed, Outcome: Denied, Actor: Anonymous, Ref: "zzzzzzzzzzzzzzzz"},
		{Kind: RateLimited, Outcome: Denied, Actor: Anonymous, Count: -1},
		{},
	}
	for i, e := range bad {
		if err := l.Log(e); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%d: %v", i, err)
		}
	}
	if buf.Len() != 0 || l.Rejected() != len(bad) {
		t.Fatalf("nothing written, all counted: %q %d", buf.String(), l.Rejected())
	}
}

func TestPseudonymIsStableKeyedAndOpaque(t *testing.T) {
	k1, k2 := []byte("operator key one"), []byte("operator key two")
	a := Pseudonym(k1, "ana@example.com")
	if a != Pseudonym(k1, "ana@example.com") || a == Pseudonym(k1, "bo@example.com") || a == Pseudonym(k2, "ana@example.com") {
		t.Fatal("stable per subject and key, different otherwise")
	}
	if strings.Contains(string(a), "ana") || len(a) != 16 {
		t.Fatalf("%q", a)
	}
	var buf bytes.Buffer
	if err := NewLogger(&buf).Log(Event{Kind: AccessGranted, Outcome: OK, Actor: Owner, Ref: a}); err != nil || !strings.Contains(buf.String(), string(a)) {
		t.Fatalf("%v %q", err, buf.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteErrorIsReturned(t *testing.T) {
	if err := NewLogger(failWriter{}).Log(Event{Kind: ConfigChanged, Outcome: OK, Actor: System}); err == nil {
		t.Fatal("a failing sink is an error")
	}
}
