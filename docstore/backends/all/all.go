package all

import (
	_ "github.com/elaous/gojiberry/docstore/backends/blob"
	_ "github.com/elaous/gojiberry/docstore/backends/mongo"
	_ "github.com/elaous/gojiberry/docstore/backends/postgres"
	_ "github.com/elaous/gojiberry/docstore/backends/s3"
)
