package saver

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Two Savers on one store stand for two server processes: each has its own lock,
// so only the conditional write keeps one from overwriting the other's catalog.
func TestTwoSaversOnOneStoreLoseNothing(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		a := &Saver{Targets: []Target{tg}}
		b := &Saver{Targets: []Target{tg}}
		var wg sync.WaitGroup
		const each = 6
		for i, s := range []*Saver{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for n := 0; n < each; n++ {
					if _, err := s.Save(ctx, tg.Name, docfile.JSON, named(fmt.Sprintf("doc-%d-%d", i, n))); err != nil {
						t.Errorf("%s: %v", tg.Name, err)
					}
				}
			}()
		}
		wg.Wait()
		live, _, err := a.Lists(ctx, tg.Name)
		if err != nil || len(live) != 2*each {
			t.Errorf("%s: %d of %d documents are listed (%v)", tg.Name, len(live), 2*each, err)
		}
	}
}

// A store that cannot write conditionally still works, as before.
type unconditional struct{ docstore.Adapter }

func TestUpdateWithoutConditionalStoreStillWrites(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "plain", Adapter: unconditional{docstore.NewMemoryAdapter()}}}}
	if _, err := s.Save(ctx, "plain", docfile.JSON, named("x")); err != nil {
		t.Fatal(err)
	}
	if live, _, _ := s.Lists(ctx, "plain"); len(live) != 1 {
		t.Fatal(live)
	}
}
