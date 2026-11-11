package components

// Sync states of a shared document, as SyncStatus names them.
const (
	SyncSynced     = "synced"     // connected, and everything typed has been sent
	SyncSending    = "sending"    // connected, with changes still on their way
	SyncConnecting = "connecting" // trying to reach the server
	SyncOffline    = "offline"
	SyncProblem    = "problem"
)

func SyncStatus(online, connecting bool, pending int, problem string) StatusItem {
	switch {
	case problem != "":
		return StatusItem{ID: "sync", State: SyncProblem, Live: true, Text: trf("sync.problem", "Not shared: %s", problem), Title: tr("sync.title", "Sharing status")}
	case online && pending > 0:
		return StatusItem{ID: "sync", State: SyncSending, Text: trn("sync.sending", "Sending %d change…", "Sending %d changes…", pending), Title: tr("sync.title", "Sharing status")}
	case online:
		return StatusItem{ID: "sync", State: SyncSynced, Text: tr("sync.synced", "All changes shared"), Title: tr("sync.title", "Sharing status")}
	case connecting:
		return StatusItem{ID: "sync", State: SyncConnecting, Text: tr("sync.connecting", "Connecting to the shared document…"), Title: tr("sync.title", "Sharing status")}
	case pending > 0:
		return StatusItem{ID: "sync", State: SyncOffline, Live: true, Text: trn("sync.offlinePending", "Offline: %d change waiting to be sent", "Offline: %d changes waiting to be sent", pending), Title: tr("sync.title", "Sharing status")}
	}
	return StatusItem{ID: "sync", State: SyncOffline, Live: true, Text: tr("sync.offline", "Offline: your changes will be shared when the connection returns"), Title: tr("sync.title", "Sharing status")}
}
