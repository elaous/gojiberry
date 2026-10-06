package docstore

// FolderSecretPrefix starts the key under which a confidential folder keeps its real name
// ("folder-secret/<folder ID>"), the encrypted record of docs/encryption-and-passkeys.md C.2.
// It is deliberately not under "catalog:": the catalog and the plain folder records stay
// readable (they hold the placeholder and the shape of the tree), while this record is an
// ordinary stored item that an encrypting adapter wraps, for people's keys only, by the
// folder's own protection level (the signed keys:<folder ID> record, as for a document).
const FolderSecretPrefix = "folder-secret/"
