package crdt

import "testing"

func TestSnapshotOnboardsNewReplica(t *testing.T) {
	seed := NewDoc("seed")
	seed.Put(Block{ID: "1", Kind: "heading", Content: "a", Order: 0})
	seed.Put(Block{ID: "2", Kind: "paragraph", Content: "b", Order: 1})
	seed.Delete("2")

	fresh := NewDoc("new-device")
	fresh.Apply(seed.Snapshot()...)

	if len(fresh.Blocks()) != 1 {
		t.Fatalf("expected 1 visible block after snapshot onboarding, got %d", len(fresh.Blocks()))
	}
	got, ok := fresh.Get("1")
	if !ok || got.Content != "a" {
		t.Fatalf("expected onboarded block 1 with content %q, got %+v (ok=%v)", "a", got, ok)
	}
	if tomb, ok := fresh.Get("2"); !ok || !tomb.Deleted {
		t.Fatalf("expected tombstone for block 2 to carry over, got %+v (ok=%v)", tomb, ok)
	}
}
