package docstore

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// putACL replaces the access list as the owner.
func (f *linkFixture) putACL(grants map[string]string, until map[string]time.Time) {
	f.t.Helper()
	b, _ := json.Marshal(map[string]any{"grants": grants, "until": until})
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc1/acl", string(b), f.owner, ""), "owner replaces the access list")
}

func (f *linkFixture) readACL() ACL {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/docs/doc1/acl", "", f.owner, "")
	f.want(http.StatusOK, rec, "owner reads the access list")
	var acl ACL
	if err := json.Unmarshal(rec.Body.Bytes(), &acl); err != nil {
		f.t.Fatal(err)
	}
	return acl
}

func TestAPersonsAccessEndsAtTheirDeadline(t *testing.T) {
	f := newLinkFixture(t)
	end := time.Unix(f.clock.Load(), 0).Add(2 * time.Hour)
	f.putACL(map[string]string{"viewer@example.com": "viewer", "stranger@example.com": "editor"}, map[string]time.Time{"viewer@example.com": end})

	f.advance(2*time.Hour - time.Second)
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", f.viewer, ""), "one second before the end")
	if _, ok := f.readACL().GrantEnds("viewer@example.com"); !ok {
		t.Error("the owner's list does not show the end")
	}
	f.advance(time.Second)
	f.want(http.StatusForbidden, f.do(http.MethodGet, "/docs/doc1", "", f.viewer, ""), "at the end")
	// Someone with no end keeps theirs, and the owner always does.
	f.want(http.StatusNoContent, f.do(http.MethodPut, "/docs/doc1", "v2", f.stranger, ""), "a grant without an end")
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", f.owner, ""), "the owner")
	acl := f.readACL()
	if _, ok := acl.Grants["viewer@example.com"]; ok {
		t.Errorf("an ended grant is still listed: %+v", acl)
	}
	// Giving access again (a new end) works from then on.
	f.putACL(map[string]string{"viewer@example.com": "viewer"}, map[string]time.Time{"viewer@example.com": time.Unix(f.clock.Load(), 0).Add(time.Hour)})
	f.want(http.StatusOK, f.do(http.MethodGet, "/docs/doc1", "", f.viewer, ""), "after being given access again")
}

func TestAPersonCannotGiveThemselvesALongerEnd(t *testing.T) {
	f := newLinkFixture(t)
	end := time.Unix(f.clock.Load(), 0).Add(time.Hour)
	f.putACL(map[string]string{"viewer@example.com": "viewer"}, map[string]time.Time{"viewer@example.com": end})
	longer, _ := json.Marshal(map[string]any{"grants": map[string]string{"viewer@example.com": "editor"}})
	f.want(http.StatusForbidden, f.do(http.MethodPut, "/docs/doc1/acl", string(longer), f.viewer, ""), "the grantee replaces the list")
	if got, _ := f.readACL().GrantEnds("viewer@example.com"); !got.Equal(end) {
		t.Errorf("the end changed to %v", got)
	}
}

func TestAnEndFarAwayIsCutAndAStrayEndIsDropped(t *testing.T) {
	f := newLinkFixture(t)
	far := time.Unix(f.clock.Load(), 0).Add(10 * 365 * 24 * time.Hour)
	f.putACL(map[string]string{"viewer@example.com": "viewer"}, map[string]time.Time{"viewer@example.com": far, "nobody@example.com": far})
	acl := f.readACL()
	if _, ok := acl.Until["nobody@example.com"]; ok {
		t.Error("an end for someone who has no access was kept")
	}
	got, _ := acl.GrantEnds("viewer@example.com")
	if limit := time.Unix(f.clock.Load(), 0).Add(MaxGrantLife); got.After(limit) {
		t.Errorf("end %v is past the longest allowed %v", got, limit)
	}
}

func TestWhoCanSeeShowsPeopleEndsAndDropsEndedOnes(t *testing.T) {
	now := time.Now()
	a := ACL{Owner: "o@example.com"}
	if err := a.GrantFor("a@example.com", RoleViewer, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantFor("b@example.com", RoleEditor, now, 0); err != nil {
		t.Fatal(err)
	}
	var withEnd int
	for _, v := range a.WhoCanSee(now) {
		if v.Kind == "person" && !v.Expires.IsZero() {
			withEnd++
			if v.ID != "a@example.com" || !v.Expires.Equal(now.Add(time.Hour).UTC().Truncate(time.Second)) {
				t.Errorf("wrong end: %+v", v)
			}
		}
	}
	if withEnd != 1 {
		t.Errorf("%d people with an end, want 1", withEnd)
	}
	for _, v := range a.WhoCanSee(now.Add(time.Hour)) {
		if v.ID == "a@example.com" {
			t.Errorf("a person whose access ended is listed: %+v", v)
		}
	}
	if got := a.Active(now.Add(time.Hour)).RoleOf("a@example.com"); got != "" {
		t.Errorf("role after the end: %q", got)
	}
	if len(a.Grants) != 2 {
		t.Error("Active changed the list it was called on")
	}
}

func TestGrantForRefusesBadLivesAndGrantKeepsTheEnd(t *testing.T) {
	now := time.Now()
	a := ACL{Owner: "o@example.com"}
	for _, life := range []time.Duration{time.Second, -time.Hour, MaxGrantLife + time.Hour} {
		if err := a.GrantFor("a@example.com", RoleViewer, now, life); err == nil {
			t.Errorf("life %v accepted", life)
		}
	}
	if err := a.GrantFor("A@Example.com", RoleViewer, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("a@example.com", RoleEditor); err != nil { // a change of role keeps the end
		t.Fatal(err)
	}
	if _, ok := a.GrantEnds("a@example.com"); !ok || a.Grants["a@example.com"] != RoleEditor {
		t.Errorf("role change lost the end: %+v", a)
	}
	if err := a.GrantFor("a@example.com", RoleEditor, now, 0); err != nil { // zero life removes it
		t.Fatal(err)
	}
	if _, ok := a.GrantEnds("a@example.com"); ok {
		t.Error("the end stayed after granting without one")
	}
	_ = a.GrantFor("a@example.com", RoleViewer, now, time.Hour)
	_ = a.Revoke("a@example.com")
	if len(a.Until) != 0 {
		t.Errorf("revoking left an end behind: %v", a.Until)
	}
}
