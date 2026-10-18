package docfile

import "testing"

func TestMapImagesCopiesAndReachesTheHistory(t *testing.T) {
	old := Document{Content: []Item{{Image: &ImageItem{URL: "data:a"}}}}
	d := Document{Content: []Item{{Image: &ImageItem{URL: "data:a"}}, {Image: &ImageItem{URL: "http://x"}}}, History: []Revision{{Saved: 1, Doc: &old}}}
	got := d.MapImages(func(u string) (string, bool) { return "ref:" + u, u == "data:a" })
	if got.Content[0].Image.URL != "ref:data:a" || got.Content[1].Image.URL != "http://x" || got.History[0].Doc.Content[0].Image.URL != "ref:data:a" {
		t.Errorf("%+v", got)
	}
	if d.Content[0].Image.URL != "data:a" || old.Content[0].Image.URL != "data:a" {
		t.Error("the original was changed")
	}
}

func TestAttachmentHash(t *testing.T) {
	h := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got, ok := AttachmentHash(AttachmentURL(h)); !ok || got != h {
		t.Error("round trip")
	}
	for _, bad := range []string{"data:x", AttachmentScheme + "abc", AttachmentScheme + h[:63] + "G"} {
		if _, ok := AttachmentHash(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
