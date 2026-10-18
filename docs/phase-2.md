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

Form fields are the first feature moved behind a flag: `exp_forms` for the fields, the Forms tab and Insert > Form fields; `exp_forms_submit` for anything that sends an answer anywhere (the web form with an address in the HTML export, `HTML.SubmitTo`). A form is for filling in, printing, sharing or sending filled; submitting is not part of the default product. The test suite runs once without the tags and once with `-tags experimental`; a feature that leaves the experiment loses its tag and its row says so.

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

- **Identity is a root key.** An Ed25519 key made on the first device; the **account id** is the hash of its public key. No name, no e-mail, no device id leaves the device.
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

### P2-8 Pack format, version 2 — design

The two open points ([opendoc-format.md](opendoc-format.md), "Open points explained"): the index of encrypted records, and large attachments as their own record kind. Both explained there, with a recommendation.

### P2-9 Licence and credits — blocked (after the actionable items)

Decided: a licence that is **not commercial**, has a **clause for commercial use**, and **requires credit** to this project and repository. To be applied once every actionable item is done.

- *Proposal:* the PolyForm Noncommercial License 1.0.0 (a standard text, with a "Required Notice" line that has to be kept, which is where the credit goes: the name, the repository address), plus a separate **commercial licence by agreement** (a `COMMERCIAL.md` saying how to ask, what it covers), plus a `NOTICE` file and a credits entry in the page's About, and in the exports' metadata only if the owner wants it.
- *Things to settle:* this is *source-available*, not open source as the Open Source Initiative defines it, and the repository and docs should not call it open source; every dependency's licence must allow the combination (the project has avoided GPL code on purpose); to relicense contributions for commercial use, contributors need to agree to it (a contributor agreement), which matters the day somebody else sends a change; and the text should be read by a lawyer before it is published. Alternatives, if the proposal does not fit: the Business Source License (not non-commercial: it allows use and turns open after a date), the Elastic License 2.0 (restricts hosting as a service, allows commercial use otherwise).

### P2-10 Maps: more — design

The map from OpenStreetMap is a build now ([status.md](status.md)). Later: offline tiles, several map styles, markers and routes, a place id that is not tied to one provider. Plus Codes (Open Location Code, an open specification) are the "unique world location id" used now; others (what3words is proprietary; Wikidata and GeoNames ids are open) can be added as inputs.

### P2-11 Voice sources, more — design

Beyond the four voices to begin with: a signed catalogue format for third-party repositories (so a repository can prove who published a voice and under what licence), per-voice licence acceptance kept with the person, voices for other languages with their phonemizers, a way to share a voice list between a team.
