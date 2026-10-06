// Command docsh is a command line for a document store: bulk rename, move and tag by
// pattern, import and export, packs, and repair (see package console for the commands).
//
//	docsh -store files:./docs ls -l 'Report *'
//	docsh -store sqlite:docs.db -n mv 'Report *' Archive/      (dry run)
//	docsh -store http://localhost:8090 -token T            (an interactive session)
//	docsh -store files:./docs -f script.txt                (stops at the first error, exits 1)
//
// A store is memory, files:DIR, sqlite:FILE.db or the URL of a docstore server.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/elaous/gojiberry/console"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "docsh:", err)
		os.Exit(1)
	}
}

func openStore(ctx context.Context, spec, token string) (saver.Target, error) {
	switch {
	case spec == "" || spec == "memory":
		return saver.Build(ctx, "docs", "Memory", docstore.Config{Backend: "memory"})
	case strings.HasPrefix(spec, "files:"):
		return saver.Build(ctx, "docs", "Files", docstore.Config{Backend: "localfs", BackendConfig: []byte(fmt.Sprintf(`{"dir":%q}`, strings.TrimPrefix(spec, "files:")))})
	case strings.HasPrefix(spec, "sqlite:"):
		return saver.Build(ctx, "docs", "SQLite", docstore.Config{Backend: "sqlite", BackendConfig: []byte(fmt.Sprintf(`{"path":%q}`, strings.TrimPrefix(spec, "sqlite:")))})
	case strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://"):
		client := http.DefaultClient
		if token != "" {
			client = &http.Client{Transport: bearer{token}}
		}
		return saver.Target{Name: "docs", Label: "Server", Adapter: docstore.NewHTTPAdapter(spec, client)}, nil
	}
	return saver.Target{}, fmt.Errorf("unknown store %q (memory, files:DIR, sqlite:FILE, or a server URL)", spec)
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func run(args []string) error {
	fl := flag.NewFlagSet("docsh", flag.ContinueOnError)
	store := fl.String("store", "memory", "the store: memory, files:DIR, sqlite:FILE, or a server URL")
	token := fl.String("token", "", "a bearer token for a server")
	dry := fl.Bool("n", false, "dry run: say what would change, change nothing")
	yes := fl.Bool("y", false, "answer yes to every question")
	script := fl.String("f", "", "run the commands in this file, one per line")
	if err := fl.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	t, err := openStore(ctx, *store, *token)
	if err != nil {
		return err
	}
	sh := console.New(&saver.Saver{Targets: []saver.Target{t}}, t.Name, os.Stdin, os.Stdout, os.Stderr)
	sh.DryRun, sh.Yes = *dry, *yes
	switch {
	case *script != "":
		f, err := os.Open(*script)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sh.In = f
		sh.Script = true // stops at the first error; a question needs -y
		return sh.Interact(ctx, false)
	case fl.NArg() > 0:
		return sh.Exec(ctx, fl.Args())
	}
	return sh.Interact(ctx, true)
}
