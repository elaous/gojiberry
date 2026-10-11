package components

import "testing"

func TestSyncStatusSaysWhetherWorkReachedOthers(t *testing.T) {
	for _, tc := range []struct {
		online, connecting bool
		pending            int
		problem            string
		state, text        string
		live               bool
	}{
		{true, false, 0, "", SyncSynced, "All changes shared", false},
		{true, false, 1, "", SyncSending, "Sending 1 change…", false},
		{true, false, 7, "", SyncSending, "Sending 7 changes…", false},
		{false, true, 0, "", SyncConnecting, "Connecting to the shared document…", false},
		{false, true, 3, "", SyncConnecting, "Connecting to the shared document…", false},
		{false, false, 0, "", SyncOffline, "Offline: your changes will be shared when the connection returns", true},
		{false, false, 1, "", SyncOffline, "Offline: 1 change waiting to be sent", true},
		{false, false, 12, "", SyncOffline, "Offline: 12 changes waiting to be sent", true},
		{true, false, 4, "refused", SyncProblem, "Not shared: refused", true},
		{false, false, 0, "too big", SyncProblem, "Not shared: too big", true},
	} {
		it := SyncStatus(tc.online, tc.connecting, tc.pending, tc.problem)
		if it.State != tc.state || it.Text != tc.text || it.Live != tc.live || it.ID != "sync" || it.Title == "" {
			t.Errorf("%+v: got %+v", tc, it)
		}
	}
}
