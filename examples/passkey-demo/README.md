# Passkey demo

Create an account with a passkey, sign in with it without typing anything, add a second
passkey and remove one (the last stays: it is the only way in).

    go run ./examples/passkey-demo/cmd/server   # builds the wasm, serves http://localhost:8080

The e2e (`go test ./examples/passkey-demo`) drives Chrome's virtual authenticator.

For a real store, `cmd/docstore` mounts `/auth/passkey/` when `GOJIBERRY_DOCSTORE_PASSKEY_RPID`,
`GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS` and `GOJIBERRY_DOCSTORE_PASSKEY_NAME` are set, and hides the
`passkey/` records from the API.
