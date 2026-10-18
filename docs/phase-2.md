# Phase 2

What is decided but not for now, what needs a design, and the rules the work follows. Each ticket has a **status**:

| Status | Meaning |
|---|---|
| design | the idea is written, the design has questions to answer before anything is built |
| ready | designed and decided; can be built |
| partial | part of it is built (it says which) |
| blocked | waiting for a person, a provider or an earlier step (named) |
| inherent | a limit that cannot be removed; the work is to reduce the exposure |
| done | built and checked |
| closed | decided not to build, with the reason |

Where a ticket also has a row in [status.md](status.md), that row is the one kept up to date; this page holds the longer text.

## Experimental features have build flags

Every experimental feature is behind a Go build tag and is off by default: a build without the tag has none of its code, its Forms-tab-like panel, its menu entries or its save format reading, and says so where it would have been. The tags:

- `experimental` switches on every experimental feature (for trying them all: `-tags experimental`);
- one tag per feature, named `exp_<feature>` (`exp_forms`, `exp_forms_submit`), so a build can take only what it wants; `experimental` implies each of them.

Form fields are the first feature moved behind a flag (done, 2026-10-18; what the tag takes out is the showcase's surface: the Forms tab, Insert > Form fields, the key panel, and the code that draws and fills a field; the library packages (`canvas.FormFieldObject`, `docfile.FormFieldItem`, `formseal`, `formdata`, the exports) stay in every build, so a document that holds fields is kept intact when a default build opens and saves it): `exp_forms` for the fields, the Forms tab and Insert > Form fields; `exp_forms_submit` for anything that sends an answer anywhere (the web form with an address in the HTML export, `HTML.SubmitTo`). A form is for filling in, printing, sharing or sending filled; submitting is not part of the default product. The test suite runs once without the tags and once with `-tags experimental` (`make e2e-test` runs the experimental tests in a second pass, `make e2e-test-experimental` alone; `make wasm-lint` lints both; try a build with `GOFLAGS=-tags=experimental`, `-tags exp_forms` or `-tags exp_forms,exp_forms_submit`; `exp_forms_submit` needs `exp_forms`); a feature that leaves the experiment loses its tag and its row says so.

## Sharing: locks and colours (decided, ready)

For several people in one document (the hub; [multiuser-design.md](multiuser-design.md)):

- **Text.** Edits and formatting of text by several people **merge** (the CRDT already does), applied after a short delay so that what a person is typing is not moved under them.
- **Objects.** Pictures, tables, form fields, equations, diagrams: when one person has an object open or is changing it, the others cannot, and see **busy** until it is released (on the person's leaving the object, or after a time without activity). Two people changing the same object at the same time is therefore not a merge: it is a wait.
- **One colour for each person** in the document, chosen once (the `color` the page already takes for the live cursor). Text that person is typing is shown in that colour; after a short delay it goes back to the normal colour. An object they hold has an outline in their colour. The **busy** mark is a small rectangle in that person's colour as its background, with the word *busy* in it, at a corner of the object; it is also said to a screen reader ("busy: Ana is changing this table"), since colour alone is not enough.
- **Built when:** the lock needs a message on the hub (`lock`, `release`, with a time-out), the objects need a stable id (they have one for comments), the page needs the colour layer; each is its own change. Tests: two pages, one holds, the other sees busy and cannot change; release; time-out.

## Tickets

### P2-1 Writing statistics (personal) — design

The numbers shown in the document list for a person's own writing: **time writing**, words added, sessions, revisions. They are a convenience for the person, not a measure of the person for anybody else.

- *Time writing.* Active time, not open time: the seconds between two edits (keystrokes, pastes, deletions) when the gap is under an idle cut-off (60 seconds by default, a preference), summed. A long gap counts for nothing, a window that is not in focus counts for nothing, and a document in view-only mode counts for nothing. Reading is not writing.
- *Words added.* The words typed or pasted, minus the ones deleted that session (can be negative in a session of cutting), counted from the document's text, not from the keys.
- *Sessions.* A run of activity; a gap of more than 30 minutes starts another. *Revisions* are the saved versions the history already has.
- *Where they are kept.* In the person's own config store (the one the preferences use), by document id; **never sent to the operator** and not part of the telemetry. Opt-in, per person. For a confidential (encrypted) document the figures are kept sealed with it or not at all; the page asks.
- *Questions left:* whether the figures follow a document that is shared (they should not: they are the person's own), and what to show for a document opened on two devices (sum, since they sync).

### P2-2 Capacity metrics for operators — design

Generic figures that help an operator see latency, throttling and scaling problems **without knowing which user**. Builds on `observe` ([observability.md](observability.md): closed allowlists, off by default, the builtin sink and `GET /admin/observe`).

- *Runtime:* latency histograms per route, throttling (requests refused by a limiter) and error counts, queue depths: partly there.
- *Storage:* total bytes allocated; the **distribution** of document sizes (bucketed by powers of two: how many documents are under 1 KB, 1 to 4 KB, and so on) and its mean; the **growth** (total bytes now against a day, a week and a month before); and the **distribution of use against quota** as a few buckets (under 50%, 50 to 80%, 80 to 95%, 95 to 100%, over), counts of users in each, no ids. From growth and the free space an instance can say "full in N days" and alert before it is.
- *What to report (decided 2026-10-18): the general distribution, not the individual.* For every figure: count, mean, **standard deviation** and the **p50, p90, p95 and p99** (from fixed buckets, so the percentile is an estimate within a bucket), for request latency per route, document size, storage per user and use against quota. A user alone in the upper tail would be singled out by a percentile of one: a bucket or a percentile over fewer than 5 users is not reported.
- *Rules:* no user id, no document id, no name in a label; counts and sums only; buckets, not values, wherever one value could single a person out (a bucket with fewer than, say, 5 users is reported as "fewer than 5"). Not added to the telemetry the page sends: these are the server's own figures, read by its operator.

### P2-3 Quota authority — design

The case: an operator runs many instances, and every user of a region gets the same quota; the instance should be able to ask "what is the quota here" without holding information about users, and handle the rest on its own.

- *A global API* answers one question: for this instance and region, what is the per-user quota, and until when. The instance enforces the quota itself, per user, in its own store, and never sends the authority a user's identity or use.
- *Instance keys.* Each instance has its own Ed25519 key (made on first start; the public part is registered with the authority, which signs a **licence** for it: `{instance id (random), region, quota, not-before, not-after}`). A request is signed by the instance's key over a fresh nonce; the answer is signed by the authority (its public key is built into the release and can be pinned in configuration). An instance that has no valid licence runs on the default free quota and says so.
- *Tampering.* What can be done: answers and licences cannot be forged (signatures), cannot be replayed (nonces, expiry), cannot be moved to another instance (bound to its key), and an instance that never checks in loses its licence at expiry. What **cannot** be done, and is *inherent*: an operator who controls the hardware can patch the program to ignore the answer. The answer to that is not in the code but in the terms (the licence, P2-9), in reproducible, signed releases for anyone who wants to check what they run, and in aggregate check-ins (the instance reports its total allocation, signed; a total far above its licence is a finding for people, not a lock). Quota enforced where the authority itself holds the storage is the only enforcement that cannot be patched out.
- *Questions left:* who runs the authority, and whether a self-hosted instance is meant to have a quota at all or only the hosted ones.

### P2-4 AI helper hub — design

A hub of agents (summarize, criticize, writer styles, "write a ..." for many kinds of document, a designer for styles and fonts), a mentor and helper, not a ghost-writer ([ai-helper-ideas.md](ai-helper-ideas.md)). Start from what exists: the `llm` add-on (a model on the device), the grammar client, the command registry and the scripting sandbox (P2-7), which already gives an agent a safe way to read and change a document through commands.

- *Questions:* where a model runs (device add-on first; a server only when the operator names one, with the text going there said plainly, as for hosted voices); what an agent may read (the open document, selected documents, links) and write (never without the person accepting: every change a suggestion, as proposals already are); how end-to-end encrypted documents are treated (the server cannot read them, so an agent on a server cannot either: only a device agent).

### P2-5 Device-chain identity — design

A sign-in that holds no information about the person. Proposal, built from what exists (passkeys, the key directory, the recovery sheet, the CRDT and hash-chain code):

- **Identity is a root key.** An Ed25519 key made on the first device; the **account id** is the hash of its public key. No name, no e-mail, no device id leaves the device. *Where the id lives:* the id is **public, not a secret** (it is a hash of a public key), and it is stored in both places: the person's devices keep the chain, and the operator keeps the id, the certificate log (opaque to it) and the access lists that name it, because a server cannot share a document with someone it cannot name. What the operator never has is the root's private key or a name. To stop two operators linking the same person, the id an operator sees is **derived for that operator** (a hash of the root public key and the operator's domain), so the same person has a different id with each.
- **Each device has its own key**, certified by a device that is already trusted: a *certificate* `{device key, added by, time}` signed by the adding device. Two devices meet by **QR code** (the new one shows its key; the trusted one scans and signs; the certificate comes back). The certificates form a small **signed, hash-linked log**, which the devices merge when they meet; the server may keep a copy as an opaque blob (it can verify the signatures, it learns no one). A revocation is another entry in the log (this device key no longer counts), and the server drops the passkey of that device.
- **Passkeys stay the sign-in** of each device (WebAuthn); the chain says which devices are one person. A document's access list stores the **account id** (the chain id), not a passkey, so adding or losing a device changes nothing in any document.
- **A wiped device.** Three ways back, in this order: (1) any other trusted device adds the new one (QR); (2) before wiping, the person can make a **transfer code** (a one-time code, short-lived, that lets the next device join without another device); (3) the **recovery sheet** (item 76), which is the printed backup of the root key, restores the identity and the person certifies a new device. Without any of the three the identity is lost; the page says so when the account is made.
- *Not a third system:* the chain is the list of devices, the recovery sheet is the backup of the root, passkeys are the sign-in. *Questions left:* how the root is stored on devices (it should not be kept on every device: a device key suffices once the chain exists; the root is kept only on the recovery sheet), and how an account moves between two operators.

### P2-6 Diagrams: open-source options — design

The picture drawn from a description is enough for now. For an editable object, open-source tools to look at (licences are from memory and are to be checked before one is chosen):

| Tool | What | Licence |
|---|---|---|
| D2 | text to diagrams, written in Go, so it could run in the program | MPL-2.0 |
| Mermaid | text to flowcharts, sequence, Gantt and more, in the browser | MIT |
| Graphviz | graph layout (WebAssembly builds exist) | EPL-1.0 |
| ELK (elkjs) | layout engine for node-and-edge diagrams | EPL-2.0 |
| diagrams.net (draw.io) | a full editor, embeddable | Apache-2.0 |
| Excalidraw | hand-drawn whiteboard | MIT |
| bpmn-js | BPMN process diagrams | bpmn.io licence (MIT-like, requires its logo to stay) |
| Cytoscape.js, React Flow, JointJS Core | node-and-edge editing as libraries | MIT, MIT, MPL-2.0 |

Next step: a small test of D2 (it is Go) and Mermaid as `diagram` add-ons, and a decision whether an editable object is wanted after all.

### P2-7 Scripting, phase 2 — design

Phase 1 (now): a sandbox with the commands of the registry, `text()` and `insert()`. Next (ready): **get/set** over what extended Markdown, text formatting, text styles, images and document metadata cover. Lift of the further features, for phase 2 (S small, M medium, L large):

| Feature | Lift | Why |
|---|---|---|
| get/set document metadata: title, language, tags, folder, page size and margins, header and footer text | S | already in `docfile` and the page setup |
| outline: list headings (read-only) | S | the outline already exists |
| comments: list (read-only), add | S / M | the comment set has the calls |
| find and replace | S | a command exists |
| apply a named style to a range; list styles | S | the styles feature exists |
| selection: get range and text, set | S | cursors are in the editing target |
| images: list with alt and legend, set alt and legend, insert from bytes | S | the Pictures tab does all of it |
| tables: get and set a cell; add a row | M | the table object has the calls; formulas stay formulas |
| export to Markdown, HTML, text and back (get/set as extended Markdown) | S / M | the transfer registry has the formats |
| undo grouping: one script run is one undo step | S | edits go through `Replace` as one change |
| per-script storage (a small key-value store) | M | needs a quota and a place |
| events (on change, on selection) with a debounce | M | the page has the signals; needs back-pressure |
| scripts that register commands (opendocs' plug-in API) | M | the registry takes commands; permission model needed |
| a dialog or panel from a script | M | needs a safe UI contract |
| network access with an allow-list of hosts | M | a security design: today there is none, on purpose |
| scheduled scripts, scripts across documents | L | needs a runner that outlives the page |
| macro recording | M | record the commands run, replay them |

### P2-8 Pack format, version 2 — partial (decided)

Decided 2026-10-18: **blur** the index of encrypted records now (an export option: sizes rounded to buckets, times blurred) and **encrypt the index as an option in phase 2** (this ticket, status design: nothing can be listed or verified without the key, so it is an option and not the default); **large attachments as their own record kind: yes, version 2**, written only when a pack has attachments over a size. The blur and version 2 are rows in [status.md](status.md) (open). Explained in [opendoc-format.md](opendoc-format.md), "Open points explained".

### P2-9 Licence and credits — blocked (after the actionable items)

Decided: a licence that is **not commercial**, has a **clause for commercial use**, and **requires credit** to this project and repository; the owner prefers "open source non commercial, with a commercial licence". To be applied once every actionable item is done.

*A point to settle first: the two words do not go together.* The Open Source Definition (the Open Source Initiative's) forbids restricting a field of use, so a licence that is **non-commercial** is **source-available**, not open source, and calling it open source would be wrong (and a reason for some people and some package hosts to refuse it). There are two honest routes:

- **A. Non-commercial, source-available:** PolyForm Noncommercial 1.0.0 plus a commercial licence by agreement (below). Commercial use needs your permission, in every case.
- **B. Truly open source with a commercial licence beside it (dual licence):** the GNU AGPL-3.0 for everyone, which lets anyone use the code, commercially too, **if they publish their own source** under the same terms, including for a service they run over a network; a company that does not want to publish pays for the commercial licence. The AGPL's section 7 lets the project require that its credit notices stay. This keeps the project open source and still gives a reason to buy a commercial licence, but it does not stop a company from using it commercially under the AGPL.

Route A matches "non commercial"; route B matches "open source". The proposal below is route A; choose before it is applied.

- *Proposal:* the PolyForm Noncommercial License 1.0.0 (a standard text, with a "Required Notice" line that has to be kept, which is where the credit goes: the name, the repository address), plus a separate **commercial licence by agreement** (a `COMMERCIAL.md` saying how to ask, what it covers), plus a `NOTICE` file and a credits entry in the page's About, and in the exports' metadata only if the owner wants it.
- *Things to settle:* this is *source-available*, not open source as the Open Source Initiative defines it, and the repository and docs should not call it open source; every dependency's licence must allow the combination (the project has avoided GPL code on purpose); to relicense contributions for commercial use, contributors need to agree to it (a contributor agreement), which matters the day somebody else sends a change; and the text should be read by a lawyer before it is published. Alternatives, if the proposal does not fit: the Business Source License (not non-commercial: it allows use and turns open after a date), the Elastic License 2.0 (restricts hosting as a service, allows commercial use otherwise).

### P2-10 Maps: more — design

The map from OpenStreetMap is a build now ([status.md](status.md)). Later: offline tiles, several map styles, markers and routes, a place id that is not tied to one provider. Plus Codes (Open Location Code, an open specification) are the "unique world location id" used now; others (what3words is proprietary; Wikidata and GeoNames ids are open) can be added as inputs.

### P2-11 Voice sources, more — design

Beyond the four voices to begin with: a signed catalogue format for third-party repositories (so a repository can prove who published a voice and under what licence), per-voice licence acceptance kept with the person, voices for other languages with their phonemizers, a way to share a voice list between a team.

### P2-12 Age checking and the data a deployment keeps — blocked (needs legal advice)

The code collects no age or birthdate. Two optional server features keep something about a person: web push (handle, the browser's push endpoint and keys; [web-push.md](web-push.md)) and mention mail (an address the host already holds). Whether a deployment must check ages depends on where it runs and who uses it, which is for a lawyer to say before launch. What the code can do meanwhile is keep less: a "forget this person" call on the push store (to honour a deletion request), and no new personal data without a row in [status.md](status.md) that says why.

### P2-13 Peer-to-peer mode — design

Today sharing goes through a hub that somebody runs (the editor itself is local-first: documents stay in the browser). A peer-to-peer mode would let two or more browsers exchange the edits directly (WebRTC data channels), with the hub reduced to introducing the peers (a signalling server, which still has to exist and sees only who is connecting, not the content, if the signalling messages are end-to-end encrypted with the document key). What it takes: signalling over the existing hub transport; ICE with STUN and, for networks that block direct links, a TURN relay (a relay is a server again, and costs bandwidth); the CRDT already merges, so the sync is the existing frames over a different channel; peers must authenticate each other (the document's owner signatures already say who may write); a person offline while the others are online catches up from whoever is there, so some peer or a hub must keep the document. Not built; decided as phase 2 (owner, 2026-10-18).
