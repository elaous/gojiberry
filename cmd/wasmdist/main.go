// Command wasmdist prepares a built wasm app for deployment.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/elaous/gojiberry/wasmserve"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// handlerFor serves dir as a deployment should.
func handlerFor(dir string) http.Handler {
	return wasmserve.Handler(os.DirFS(dir), wasmserve.Options{Fallback: "index.html", ServiceWorker: true})
}

func run(args []string, out io.Writer) error {
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	fs := flag.NewFlagSet("wasmdist", flag.ContinueOnError)
	brotli := fs.String("brotli", "auto", `the brotli tool: "auto" finds it on the PATH and writes only .gz when it is missing, "off" writes only .gz, or a path`)
	minSize := fs.Int64("min", 1024, "leave files smaller than this many bytes alone")
	sw := fs.Bool("sw", true, "write sw.js, a service worker that keeps the app (index.html, wasm_exec.js, main.wasm) in the browser by the version of its contents")
	serve := fs.String("serve", "", "after compressing, serve the directory on this address (for example localhost:8080)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: wasmdist [-brotli auto|off|PATH] [-min BYTES] [-serve ADDR] DIR")
	}
	dir := fs.Arg(0)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("wasmdist: %s is not a directory", dir)
	}
	if *sw {
		script, err := wasmserve.ServiceWorker(os.DirFS(dir), nil)
		switch {
		case err != nil:
			say("wasmdist: no sw.js written (%v)\n", err)
		default:
			if err := os.WriteFile(filepath.Join(dir, "sw.js"), script, 0o644); err != nil {
				return err
			}
			say("wasmdist: wrote sw.js, the service worker that keeps %s, %s and %s for repeat visits\n", wasmserve.DefaultShell[0], wasmserve.DefaultShell[1], wasmserve.DefaultShell[2])
		}
	}
	opt := wasmserve.PrecompressOptions{MinSize: *minSize}
	switch *brotli {
	case "off":
	case "auto":
		if p, err := exec.LookPath("brotli"); err == nil {
			opt.Brotli = p
		} else {
			say("%s\n", "wasmdist: the brotli tool is not installed, so only .gz files are written (a .br is about 15% smaller)")
		}
	default:
		opt.Brotli = *brotli
	}
	done, err := wasmserve.Precompress(dir, opt)
	if err != nil {
		return err
	}
	var orig, gz, br int64
	for _, c := range done {
		if c.Action == "removed" {
			say("wasmdist: removed %s (%s)\n", c.Name+map[string]string{"gzip": ".gz", "br": ".br"}[c.Encoding], "no gain or no longer eligible")
			continue
		}
		say("wasmdist: %-5s %-30s %9d -> %9d bytes (%s)\n", c.Action, c.Name, c.Original, c.Size, c.Encoding)
		if c.Encoding == "gzip" {
			orig += c.Original
			gz += c.Size
		} else {
			br += c.Size
		}
	}
	if orig > 0 {
		say("wasmdist: %d bytes of files, %d as gzip", orig, gz)
		if br > 0 {
			say(", %d as brotli", br)
		}
		say("\n")
	}
	if *serve != "" {
		say("wasmdist: serving %s on http://%s/\n", dir, *serve)
		return http.ListenAndServe(*serve, handlerFor(dir))
	}
	return nil
}
