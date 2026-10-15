//go:build !(js && wasm)

package e2etest

import (
	"context"
	"fmt"
	"sort"

	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/chromedp"
)

// Profile runs action with the browser's CPU profiler on and returns the n functions with the most time of
// their own, then those with the most time with everything they called, as "ms  function (url:line)" lines, for finding where the time of an action goes.
func (d *Tab) Profile(action func(), n int) []string {
	d.t.Helper()
	d.must(chromedp.ActionFunc(func(ctx context.Context) error { return profiler.Enable().Do(ctx) }))
	d.must(chromedp.ActionFunc(func(ctx context.Context) error { return profiler.Start().Do(ctx) }))
	action()
	var prof *profiler.Profile
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		prof, err = profiler.Stop().Do(ctx)
		return err
	}))
	self := map[int64]float64{}
	for i, id := range prof.Samples {
		if i < len(prof.TimeDeltas) {
			self[id] += float64(prof.TimeDeltas[i]) / 1000
		}
	}
	byName := map[string]float64{}
	name := func(nd *profiler.ProfileNode) string {
		cf := nd.CallFrame
		return fmt.Sprintf("%s (%s:%d)", cf.FunctionName, cf.URL, cf.LineNumber)
	}
	for _, nd := range prof.Nodes {
		byName[name(nd)] += self[nd.ID]
	}
	// the time of a function with everything it called, counted once per call path (not again for recursion)
	byID := map[int64]*profiler.ProfileNode{}
	for _, nd := range prof.Nodes {
		byID[nd.ID] = nd
	}
	var total func(nd *profiler.ProfileNode) float64
	total = func(nd *profiler.ProfileNode) float64 {
		t := self[nd.ID]
		for _, c := range nd.Children {
			if ch := byID[c]; ch != nil {
				t += total(ch)
			}
		}
		return t
	}
	incl := map[string]float64{}
	var walk func(nd *profiler.ProfileNode, seen map[string]bool)
	walk = func(nd *profiler.ProfileNode, seen map[string]bool) {
		nm := name(nd)
		again := seen[nm]
		if !again {
			incl[nm] += total(nd)
			seen[nm] = true
		}
		for _, c := range nd.Children {
			if ch := byID[c]; ch != nil {
				walk(ch, seen)
			}
		}
		if !again {
			delete(seen, nm)
		}
	}
	if len(prof.Nodes) > 0 {
		walk(prof.Nodes[0], map[string]bool{})
	}
	type kv struct {
		k string
		v float64
	}
	var all []kv
	for k, v := range byName {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, fmt.Sprintf("self %8.1f  %s", all[i].v, all[i].k))
	}
	all = all[:0]
	for k, v := range incl {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	for i := 0; i < len(all) && i < 2*n; i++ {
		out = append(out, fmt.Sprintf("incl %8.1f  %s", all[i].v, all[i].k))
	}
	return out
}
