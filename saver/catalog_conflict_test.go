//go:build !js

package saver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

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

func TestTwoProcessesOnOneSQLiteFileLoseNothing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")
	var savers []*Saver
	for range 2 {
		a, err := docstore.NewSQLiteAdapter(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		savers = append(savers, &Saver{Targets: []Target{{Name: "db", Adapter: a}}})
	}
	var wg sync.WaitGroup
	const each = 8
	for i, s := range savers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < each; n++ {
				if _, err := s.Save(ctx, "db", docfile.JSON, named(fmt.Sprintf("p%d-%d", i, n))); err != nil {
					t.Errorf("process %d: %v", i, err)
				}
			}
		}()
	}
	wg.Wait()
	if live, _, err := savers[0].Lists(ctx, "db"); err != nil || len(live) != 2*each {
		t.Errorf("%d of %d documents listed (%v)", len(live), 2*each, err)
	}
}

func TestTwoProcessesOnOneDirectoryLoseNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var savers []*Saver
	for range 2 {
		a, err := docstore.NewLocalFSAdapter(dir)
		if err != nil {
			t.Fatal(err)
		}
		savers = append(savers, &Saver{Targets: []Target{{Name: "fs", Adapter: a}}})
	}
	var wg sync.WaitGroup
	const each = 8
	for i, s := range savers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < each; n++ {
				if _, err := s.Save(ctx, "fs", docfile.JSON, named(fmt.Sprintf("q%d-%d", i, n))); err != nil {
					t.Errorf("process %d: %v", i, err)
				}
			}
		}()
	}
	wg.Wait()
	if live, _, err := savers[0].Lists(ctx, "fs"); err != nil || len(live) != 2*each {
		t.Errorf("%d of %d documents listed (%v)", len(live), 2*each, err)
	}
}

func TestPeopleSharingAStoreHaveTheirOwnCatalogs(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	mk := func(who string) *Saver {
		return &Saver{Targets: []Target{{Name: "shared", Adapter: mem, CatalogKey: UserCatalogKey(who)}}}
	}
	ana, bob := mk("ana@x.com"), mk("bob@x.com")
	if UserCatalogKey("ana@x.com") == UserCatalogKey("bob@x.com") || strings.Contains(UserCatalogKey("ana@x.com"), "ana") {
		t.Fatal("keys differ and hide the identifier")
	}
	_, _ = ana.Folders(ctx, "shared")
	_, _ = bob.Folders(ctx, "shared")
	if _, err := bob.Save(ctx, "shared", docfile.JSON, named("bob's doc")); err != nil {
		t.Fatal(err)
	}
	af, _ := ana.CreateFolder(ctx, "shared", "", "Ana's")
	d := named("ana's doc")
	d.SetFolder(af.ID)
	if _, err := ana.Save(ctx, "shared", docfile.JSON, d); err != nil {
		t.Fatal(err)
	}
	if live, _, folders, _ := bob.Overview(ctx, "shared"); len(live) != 1 || live[0].Name != "bob's doc" || len(folders) != 0 {
		t.Errorf("bob sees %+v %+v", live, folders)
	}
	if live, _, folders, _ := ana.Overview(ctx, "shared"); len(live) != 1 || live[0].Name != "ana's doc" || len(folders) != 1 {
		t.Errorf("ana sees %+v %+v", live, folders)
	}
	if err := bob.Rebuild(ctx, "shared"); err != nil {
		t.Fatal(err)
	}
	if folders, _ := bob.Folders(ctx, "shared"); len(folders) != 0 {
		t.Errorf("Ana's folder showed up for Bob: %+v", folders)
	}
	if folders, _ := ana.Folders(ctx, "shared"); len(folders) != 1 {
		t.Errorf("%+v", folders)
	}
}
