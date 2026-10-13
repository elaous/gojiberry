package migrate

import "github.com/elaous/gojiberry/docximport"

// The tests read Word files with the real reader.
func init() { DefaultImport = docximport.Import }
