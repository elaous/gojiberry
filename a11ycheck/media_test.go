package a11ycheck

import (
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
)

func rulesOf(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule+":"+f.Severity.String())
	}
	return out
}

func TestMediaFindingsRequireCaptionsAndTranscripts(t *testing.T) {
	vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nHello there.\n"
	for _, c := range []struct {
		name string
		m    docfile.MediaItem
		want []string
	}{
		{"a video with nothing", docfile.MediaItem{URL: "a.mp4"}, []string{"media-captions:error", "media-transcript:warning", "media-title:warning"}},
		{"a titled video with nothing", docfile.MediaItem{URL: "a.mp4", Title: "Talk"}, []string{"media-captions:error", "media-transcript:warning"}},
		{"a video with captions: their words are the transcript", docfile.MediaItem{URL: "a.mp4", Title: "Talk", Captions: vtt}, nil},
		{"a video with a caption address and a transcript", docfile.MediaItem{URL: "a.mp4", Title: "Talk", CaptionsURL: "a.vtt", Transcript: "Hello."}, nil},
		{"a video with a caption address only: the words are not known", docfile.MediaItem{URL: "a.mp4", Title: "Talk", CaptionsURL: "a.vtt"}, []string{"media-transcript:warning"}},
		{"a video with a transcript but no captions", docfile.MediaItem{URL: "a.mp4", Title: "Talk", Transcript: "Hello."}, []string{"media-captions:error"}},
		{"audio with nothing", docfile.MediaItem{URL: "a.mp3", Kind: "audio", Title: "Talk"}, []string{"media-transcript:error"}},
		{"audio with a transcript", docfile.MediaItem{URL: "a.mp3", Kind: "audio", Title: "Talk", Transcript: "Hello."}, nil},
		{"audio with captions", docfile.MediaItem{URL: "a.mp3", Kind: "audio", Title: "Talk", Captions: vtt}, nil},
		{"audio with a blank transcript and no title", docfile.MediaItem{URL: "a.mp3", Kind: "audio", Transcript: "  \n "}, []string{"media-transcript:error", "media-title:warning"}},
		{"captions with no words in them", docfile.MediaItem{URL: "a.mp4", Title: "Talk", Captions: "WEBVTT\n\n"}, []string{"media-captions:error", "media-transcript:warning"}},
	} {
		got := rulesOf(MediaFindings(c.m, 0, 0))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for _, f := range MediaFindings(docfile.MediaItem{URL: "a.mp4"}, 0, 0) {
		if f.Message == "" || f.Fix == "" || f.Action != ActionWriteMediaText {
			t.Errorf("a finding must say what is wrong and how to fix it: %+v", f)
		}
	}
}

func TestCheckFindsMediaInTheDocument(t *testing.T) {
	d := docfile.Document{Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "Intro\n"}},
		{Media: &docfile.MediaItem{URL: "talk.mp4", Title: "Talk"}},
		{Chars: &docfile.Chars{Text: "Text after\n"}},
		{Media: &docfile.MediaItem{URL: "ok.mp3", Kind: "audio", Title: "Fine", Transcript: "Words."}},
	}}
	fs := Check(d, Options{})
	got := rulesOf(fs)
	if want := []string{"media-captions:error", "media-transcript:warning"}; !slices.Equal(got, want) {
		t.Fatalf("findings %v, want %v", got, want)
	}
	for _, f := range fs {
		if f.Offset != 6 || f.Item != 1 || f.Text != "Talk" {
			t.Errorf("the clip is at offset 6, item 1: %+v", f)
		}
	}
	d.Content = append(d.Content, docfile.Item{Chars: &docfile.Chars{Text: "More\n"}}, docfile.Item{Media: &docfile.MediaItem{URL: "late.mp4", Title: "Late"}})
	late := 0
	for _, f := range Check(d, Options{}) {
		if f.Text == "Late" {
			late++
			if want := 6 + 1 + len("Text after\n") + 1 + len("More\n"); f.Offset != want || f.Item != 5 {
				t.Errorf("the second clip: offset %d item %d, want %d, 5", f.Offset, f.Item, want)
			}
		}
	}
	if late != 2 {
		t.Errorf("findings for the second clip: %d, want 2", late)
	}
	ok := docfile.Document{Content: []docfile.Item{{Media: &docfile.MediaItem{URL: "talk.mp4", Title: "Talk", Captions: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHi\n"}}}}
	if fs := Check(ok, Options{}); len(fs) != 0 {
		t.Errorf("a captioned video: %v", rulesOf(fs))
	}
	allow, errs := GateBlock.Decide(Check(d, Options{}), false)
	if allow || errs != 2 {
		t.Errorf("the block gate must stop a video with no captions: allow %v, errors %d", allow, errs)
	}
}

func docOfItems(items ...docfile.Item) docfile.Document {
	return docfile.Document{Title: "Clips", Content: items}
}

func exportText(t *testing.T, d docfile.Document) string {
	t.Helper()
	r := transfer.Default()
	export.Register(r)
	_, data, _, err := r.Export("text", d)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestACheckedClipHasItsWordsInTheExportAndAnUncheckedOneIsRefused(t *testing.T) {
	good := docOfItems(docfile.Item{Media: &docfile.MediaItem{URL: "a.mp4", Title: "Clip", Captions: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello world.\n"}})
	bad := docOfItems(docfile.Item{Media: &docfile.MediaItem{URL: "a.mp4", Title: "Clip"}})
	if fs := Check(good, Options{}); len(fs) != 0 {
		t.Fatalf("the captioned clip has findings: %v", fs)
	}
	if out := string(exportText(t, good)); !strings.Contains(out, "Hello world.") {
		t.Errorf("the checked clip's words are not in the text export: %q", out)
	}
	allow, errs := GateBlock.Decide(Check(bad, Options{}), false)
	if allow || errs != 1 {
		t.Errorf("the block gate must refuse a video with no captions: allow %v errors %d", allow, errs)
	}
	if out := string(exportText(t, bad)); strings.Contains(out, "Transcript") {
		t.Errorf("an export must not invent a transcript: %q", out)
	}
}
