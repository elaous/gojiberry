# Configuration

How a Gojiberry server is configured, and why. The short version: **environment variables are the
one contract**, secrets arrive as **files named by a `…_FILE` variable**, and nothing secret is in the
repository, in a log line, or in an error message.

## The strategy

| Option | Used here? | Why |
|---|---|---|
| Environment variables (`GOJIBERRY_*`) | **Yes, the only runtime contract** | Works the same in a container, systemd, a PaaS and a test; the servers already read them (`docstore.ConfigFromEnv`); no parser, no file format to attack; easy to override per deployment. |
| `NAME_FILE` for secrets | **Yes, preferred for every secret** | A mounted file (Docker/Kubernetes secret, systemd `LoadCredential`) readable only by the service keeps the value out of process listings, `/proc/<pid>/environ` readers and crash dumps. Setting both `NAME` and `NAME_FILE` is an error. |
| A secret manager (Vault, a cloud KMS/secret service) | Yes, through the file form | The platform fetches the secret and mounts it as a file; the code does not need to know. |
| Env file (`.env`) | Local development only | Convenient, but it is a plain file of secrets. Keep it out of git (`.gitignore`), never in an image, never in CI logs. |
| TOML | Not now | The best format if structured, hand-written configuration ever grows (comments, no indentation traps). Use it for non-secret structure then, with every key still overridable by an environment variable. |
| YAML | No | Indentation and implicit typing surprises (`no` becomes false, `1.10` becomes 1.1, the "Norway problem") in a file people edit under pressure. |
| JSON | No | No comments; fine for machines, poor for operators. |
| Protobuf text / wire | No | The text form suits schema-checked data (documents use it), but nobody expects to configure a server with it; the wire form cannot be read or diffed by an operator. |

Rules that go with it:

- **Validate everything at start.** A server that is misconfigured fails to start with a message
  naming the variable; it does not fail on the first e-mail. (`mail.FromEnv` does this.)
- **Errors name the variable, never the value.** Secrets are not in error text, logs or `String()`.
- **Unsafe combinations are refused**, not warned about (for example SMTP without TLS to a
  non-loopback host).
- **Defaults are safe and boring**; a feature that needs a credential is off until it is given one
  (`ErrNotConfigured`).

## E-mail (`mail`)

Used for address verification (roadmap 41) and sign-in notices. Two providers behind one interface
(`mail.Sender`): any SMTP server, or [Resend](https://resend.com).

| Variable | Meaning |
|---|---|
| `GOJIBERRY_MAIL_PROVIDER` | `smtp` or `resend`. Empty: no e-mail features. |
| `GOJIBERRY_MAIL_FROM` | The sender address, a plain `user@host` (for Resend, on a domain verified there). |
| `GOJIBERRY_MAIL_SMTP_HOST` | SMTP server host. |
| `GOJIBERRY_MAIL_SMTP_PORT` | Default 587 (`starttls`), 465 (`tls`), 25 (`none`). |
| `GOJIBERRY_MAIL_SMTP_TLS` | `starttls` (default; fails if the server does not offer it, never downgrades), `tls` (from the first byte), or `none` (loopback hosts only: a mail catcher or a relay on the same machine). |
| `GOJIBERRY_MAIL_SMTP_USERNAME` | Sign-in name. Set with the password, or neither (open relay). AUTH PLAIN is only used over TLS. |
| `GOJIBERRY_MAIL_SMTP_PASSWORD` / `_PASSWORD_FILE` | The password, or the path of the file that holds it. |
| `GOJIBERRY_MAIL_RESEND_API_KEY` / `_API_KEY_FILE` | The `re_…` key, or the path of the file that holds it. |

Example (Kubernetes-style secret mounted as a file):

```
GOJIBERRY_MAIL_PROVIDER=smtp
GOJIBERRY_MAIL_FROM=noreply@example.com
GOJIBERRY_MAIL_SMTP_HOST=smtp.example.com
GOJIBERRY_MAIL_SMTP_USERNAME=mailer
GOJIBERRY_MAIL_SMTP_PASSWORD_FILE=/run/secrets/smtp-password
```

In code: `s, err := mail.FromEnv(os.Getenv)`; `errors.Is(err, mail.ErrNotConfigured)` means e-mail is
simply off. `s.Send(ctx, mail.Message{To: ..., Subject: ..., Text: ..., HTML: ...})`. A message goes
to at most 10 plain addresses (no names, lists or line breaks, so nothing a person typed becomes a
header), with a text part and an optional HTML part. `Send` returns when the provider has accepted
the message, which is not the same as delivery.

## Running everything: `docker-compose.yml`

`make stack-up` starts PostgreSQL, the docstore server (on Postgres), the collaboration hub, the sync, font and
translation servers, Mailpit (a mail catcher, web UI on http://localhost:8025) and the canvas editor showcase
(http://localhost:8000). `make stack-down` stops it.

Values come from three places, strongest first: the **shell environment**, then **`.env.local`** (copy
`.env.local.example`; git-ignored), then the **development default** in the compose file. So `.env.local`
fills what the shell leaves unset, and the shell wins over the file. How it works: the make target passes
`--env-file .env.local` (when the file exists) so `${VAR:-default}` in the compose file reads it, each
service lists the variables it reads by name under `environment:` (a name alone passes the shell's value
through, and defines nothing if the shell has none), and `env_file:` with `required: false` hands the rest of
the file to the containers (needs Compose 2.24 or newer). For secrets as files, put them under `./secrets`
(git-ignored, mounted read-only at `/run/secrets`) and set the `…_FILE` variable.
