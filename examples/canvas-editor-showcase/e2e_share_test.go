//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestShareAndStats drives the word count and "share the selection": the counts
// in the status bar and the Stats tab (for the text and for the selection), and
// a link to the selected text that selects it again when opened.
func TestShareAndStats(t *testing.T) {
	site := e2etest.NewSite(t)
	d := site.Open(t, "")
	s := d.State()
	stats, _ := json.Marshal(s["stats"])
	d.OK(s.Num("stats.words") > 50 && s.Num("stats.characters") > s.Num("stats.words") && s.Num("stats.charactersNoSpaces") < s.Num("stats.characters") && s.Num("stats.paragraphs") >= 4, "counts: "+string(stats))
	d.OK(d.Is("s.stats.words === s.text.split(/\\s+/).filter(Boolean).length"), "the word count agrees with the text")
	d.OK(regexp.MustCompile(`^\d+ of 3 pages$`).MatchString(d.Text(`.gj-status-item[data-item="pages"]`)) &&
		regexp.MustCompile(`^[\d,]+ words$`).MatchString(d.Text(`.gj-status-item[data-item="words"]`)) &&
		regexp.MustCompile(`^[\d,]+ characters$`).MatchString(d.Text(`.gj-status-item[data-item="characters"]`)), "the status bar shows pages, words and characters")
	d.OK(!s.Bool("stats.hasSelection"), "nothing is selected at first")

	d.Click(`button[data-tab="stats"]`)
	d.OK(d.Count("#stats tr") == 8, "the Stats tab lists the counts")

	d.CanvasDblClick(240, 164) // selects a word
	s = d.State()
	stats, _ = json.Marshal(s["stats"])
	d.OK(s.Bool("stats.hasSelection") && s.Int("stats.selWords") == 1, "selecting a word counts it: "+string(stats))
	d.WaitFor("the selection in the status bar", "(document.querySelector('.gj-status-item[data-item=\"selection\"]') || {textContent: ''}).textContent.startsWith('Selection: 1 words')")
	d.OK(true, "the status bar shows the selection")

	d.Click(`button[data-tab="share"]`)
	link := d.Value("#share-link")
	quote := d.Text("#share-quote")
	d.OK(strings.Contains(link, "sel=") && strings.Contains(link, "q=") && !strings.Contains(link, "token"), "the link carries the selection: "+link)
	d.OK(quote != "" && !regexp.MustCompile(`\s`).MatchString(quote), "and shows the quoted word: "+quote)

	other := site.Open(t, link[strings.Index(link, "?"):])
	other.WaitForTimeout("the link to be applied", `JSON.parse(document.getElementById('state').textContent).stats.hasSelection`, 8*time.Second)
	s = other.State()
	d.OK(s.Int("stats.selWords") == 1 && s.Str("tab") == "share", "opening the link selects the same word")
	d.OK(other.Text("#share-quote") == quote, "and it is the quoted one")
	other.Close()

	d.Click(`button[data-tab="stats"]`) // clicking elsewhere keeps the selection
	d.CanvasClick(240, 300)
	d.Wait("!s.stats.hasSelection")
	d.Click(`button[data-tab="share"]`)
	d.OK(d.Count("#share-hint") == 1, "with nothing selected the Share tab says to select text")
	d.NoErrors()
}
