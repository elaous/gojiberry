// Package export writes a Snapshot as one file per format.
package export

// Exporter converts a Snapshot into one format's file bytes.
type Exporter interface {
	// Export renders snap in this Exporter's format as file bytes.
	Export(snap Snapshot) ([]byte, error)
}
