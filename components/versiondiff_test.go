package components

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docdiff"
	"github.com/elaous/gojiberry/export"
)

func TestVersionDiffSaysWhatChanged(t *testing.T) {
	changes := []docdiff.Change{
		{Op: docdiff.Equal, Kind: export.Paragraph, Text: "same"},
		{Op: docdiff.Equal, Kind: export.Paragraph, Text: "same too"},
		{Op: docdiff.Insert, Kind: export.Heading2, Level: 2, Text: "New part"},
		{Op: docdiff.Delete, Kind: export.Paragraph, Text: "gone"},
		{Op: docdiff.Edit, Kind: export.Paragraph, Text: "the red fox", Runs: []docdiff.Run{
			{Op: docdiff.Equal, Text: "the "}, {Op: docdiff.Delete, Text: "brown "}, {Op: docdiff.Insert, Text: "red "}, {Op: docdiff.Equal, Text: "fox"}}},
	}
	html := render(t, VersionDiff(VersionDiffProps{Changes: changes}))
	for _, want := range []string{
		"3 changes: 1 inserted, 1 deleted, 1 edited", `role="status"`, `aria-label="Changes between the two versions"`,
		"Inserted heading 2: ", "Deleted paragraph: ", "Edited paragraph: ", "<ins>New part</ins>", "<del>gone</del>", "<del>brown </del>", "<ins>red </ins>",
		"2 blocks unchanged", `data-op="inserted"`, `data-level="2"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, ">same<") {
		t.Error("unchanged blocks are collapsed by default")
	}
	with := render(t, VersionDiff(VersionDiffProps{Changes: changes, ShowUnchanged: true}))
	if !strings.Contains(with, "same too") || strings.Contains(with, "blocks unchanged") {
		t.Error("ShowUnchanged lists them")
	}
}

func TestVersionDiffOfTwoEqualVersions(t *testing.T) {
	html := render(t, VersionDiff(VersionDiffProps{Changes: []docdiff.Change{{Op: docdiff.Equal, Text: "x"}}}))
	if !strings.Contains(html, "The two versions are the same.") || strings.Contains(html, "<ol") {
		t.Errorf("%s", html)
	}
	one := render(t, VersionDiff(VersionDiffProps{Changes: []docdiff.Change{{Op: docdiff.Insert, Text: "x"}}}))
	if !strings.Contains(one, "1 change: 1 inserted") {
		t.Errorf("singular: %s", one)
	}
}

func TestVersionsListShowsNamesPinsAndAuthors(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	noop := func(context.Context, string, string) error { return nil }
	html := render(t, Versions(VersionsProps{Name: "plan", Pending: &Pending{},
		Versions: []DocVersion{{Key: "versions/x/1.json", Time: when, Size: 10, Name: "Sent out", Pinned: true, Author: "ana@example.com"}, {Key: "versions/x/2.json", Time: when.Add(-time.Hour), Size: 9}},
		Rename:   noop, Pin: func(context.Context, string, bool) error { return nil }, Delete: func(context.Context, string) error { return nil }}))
	for _, want := range []string{`value="Sent out"`, "by ana@example.com", `class="gj-versions-pin"`, `checked="checked"`, `aria-label="Keep the version of Sent out (2026-03-04 05:06:07)"`, `aria-label="Name of the version of 2026-03-04 05:06:07"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Count(html, `disabled="disabled"`) != 1 {
		t.Errorf("one delete button is disabled (the pinned version's): %d", strings.Count(html, `disabled="disabled"`))
	}
	plain := render(t, Versions(VersionsProps{Name: "plan", Pending: &Pending{}, Versions: []DocVersion{{Key: "k", Time: when, Name: "Just text"}}}))
	if !strings.Contains(plain, `gj-versions-name-text">Just text`) || strings.Contains(plain, "<input") {
		t.Errorf("without Rename the name is text: %s", plain)
	}
}
