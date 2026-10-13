# Atypical-speech support and Project Euphonia: what a Go port can be

Roadmap item 56. This note records what Google's `project-euphonia-app` repository contains (as read on
2026-10-12), under which licence, what is and is not open, and what the Go package `speechcoach` and the
showcase "Speech" tab therefore do and do not cover.

Status: the package `speechcoach`, the `prompt` and `language` fields of the voiceserver and this note
are in; the showcase Speech tab is a second change (it is described below as the intended whole).

## What the repository is

`https://github.com/google/project-euphonia-app` is the developer toolkit of Project Euphonia, a Google
Research effort (since 2019) on speech recognition for people with non-standard speech. Its README says it
is for developers to customise, not for use unmodified for diagnosis or treatment, and that developers are
responsible for substantial changes. It has three parts:

| Part | What it is | Language |
| --- | --- | --- |
| The app (`lib/`, `android/`, `ios/`, `assets/`) | A Flutter mobile app. It shows phrases (`assets/phrases.txt`, 100 English; an Italian example), records the person saying each one, uploads the audio and the phrase text to a Firebase Storage bucket that the developer creates, and sends recordings to a transcription service whose address is typed into its Settings. `lib/src` has `modes`, `repos`, `settings` and `ui`; localisation is by ARB files. | Dart (Flutter) |
| Training notebook (`training_colabs/Project_Euphonia_Finetuning.ipynb`) | Data preparation, fine-tuning an open-source ASR model on the collected phrases, and evaluation, in Google Colab. | Python |
| Example service (`api/`) | A small web service that transcribes with Whisper (`app_whisper.py`, `app_faster_whisper.py`), Dockerfile for Cloud Run, and an empty folder `custom_tiny_whisper_model/` where the developer puts their own fine-tuned weights. | Python |

Read: the repository page, its README, the directory listings of the top level, `lib`, `lib/src`, `api`
and `training_colabs`, and the licence text. Not read: the Dart or Python sources and the notebook cells.
That is deliberate (see "Clean room" below).

## Licence

Apache License 2.0 (the `LICENSE` file is the unmodified standard text; no copyright holder is named in
it). No `NOTICE` file is listed. Apache-2.0 allows reuse, modification and redistribution provided that
the licence text is kept, changed files are marked, copyright, patent, trademark and attribution notices of
the source are kept, and the content of a `NOTICE` file (if one existed) is carried along. It gives no right
to the names or logos "Google", "Project Euphonia" or "Project Relate".

## What is open and what is not

Open: the app source, the training notebook, the example service, the phrase lists.

Not in the repository:

- **A trained model.** There are no weights; the developer must supply their own. A model trained on
  one person's recordings is that person's data and is not shared.
- **The base model.** The notebook fine-tunes "an open-source ASR model"; the service uses Whisper.
- **The Euphonia data set.** It is not available outside Google. Google points researchers to the Speech
  Accessibility Project (University of Illinois, English) for shared data.
- **A backend.** There is no hosted service: no Firebase project, no keys, no signing profiles. The README
  tells the developer to open the storage rules to the public for the demo and to add authentication
  later; this project does not copy that.
- **Project Relate.** The consumer app (Listen, Repeat, Assistant) is a separate Google product. It has no
  public API or SDK, and a personal model cannot be exported from it. Nothing in this repository talks to it.
- **A consent process.** The README describes the data as "volunteered" but the repository describes no
  consent flow beyond that.

## What a Go port can honestly cover

A speech model that fits an individual cannot be produced by Go code in a browser page or by this
repository: it needs fine-tuning of a neural network on a GPU, which is what the notebook does. So the port
covers the flow around it, and the parts that help without any training.

Covered, in package `speechcoach` and the showcase Speech tab, all off until the person opens the tab and
all local first:

1. **Phrase sets and a recording session.** Built-in sets (written for this project, not copied from the
   Google lists) in en, de, fr and es; a person's own list (one phrase a line). Per phrase: record, listen,
   record again as often as wanted, skip, go back, continue another day. No time limit anywhere: recording
   stops only when the person stops it (the one limit is memory, ten minutes a take, and reaching it keeps
   what was said).
2. **Quality hints from simple signal analysis** on 16 kHz mono PCM: silence, too short, too quiet,
   clipping, a noisy room, a long pause. They are advice in plain words; a take can always be kept.
3. **Recording in the browser** with `getUserMedia` and an `AudioWorklet` (through `domrt/browser`),
   resampled in Go to 16 kHz.
4. **A local dataset**: recordings and transcripts in the browser's IndexedDB, sealed (AES-256-GCM) under
   the encrypted store's key when that is unlocked, else under the per-device key the search index uses.
   Export as a zip (`manifest.json`, `data.jsonl`, `audio/<id>.wav`, 16 kHz mono 16-bit) and import it
   again, with checksums and size limits. This is the same shape other tools read, so the data can leave
   for fine-tuning with the Euphonia notebook, or whisper tooling, at the person's own choice.
5. **Personalisation without training.** (a) A vocabulary of the person's own words plus the words from
   their corrections becomes whisper's initial prompt (`voice/listen.Options`, accepted by
   `cmd/voiceserver` as the form fields `prompt` and `language`). (b) A correction memory: when the person
   fixes a recognised word, the pair is kept and offered as a suggestion next time; the words they meant
   feed the autocomplete list (roadmap 79, `complete.Index.AddVocabulary`).
6. **Bring your own recogniser**: a client for any endpoint with the voiceserver contract (multipart WAV in,
   `{"text"}` out), with an optional bearer token the person types. A personalised model hosted elsewhere
   can be used this way.
7. **Optional research contribution**, per sitting, explicit and revocable (below).

Not covered, and why:

- **Training or fine-tuning a personal model**, on device or in the page: needs a GPU and an ML framework;
  not feasible in this wasm build. Options: fine-tune outside (the Euphonia notebook, or whisper.cpp
  tooling) on the exported zip and serve the result behind the recogniser contract; or use a hosted
  model the operator runs.
- **Calling Project Relate or a Euphonia model**: no public interface exists.
- **A promise that vocabulary bias fixes recognition.** It helps with names and rare words. It does not
  change how a recogniser hears an atypical voice. Testing with people with atypical speech (roadmap 56
  step 6) has not been done.
- **A built-in recogniser in the page.** The browser Web Speech API sends audio to the browser vendor and
  is not used here; transcription needs a voiceserver or another endpoint the person chooses.

## Contribution to research (privacy)

Nothing leaves the device by default. There is no default endpoint and never a third party's: the operator
configures the address (`?research=https://...` on the page, https or localhost only). The person is shown
the package before anything is sent: each recording with its phrase and transcript, the fields, the
request headers, the size and a digest. They confirm that exact package for this sitting; consent is not
stored and ends when the page closes. The package has no name, account, address, device, user agent
detail or date: recording IDs are new random ones, dates are fixed, the request has no cookie, no sign-in
token, no referrer. A random token per package lets the operator delete it; "Withdraw consent" sends
`DELETE <endpoint>/<token>`. After sending, only the operator honouring that request removes the data, and
the screen says so before the person confirms. The reuse of the Privacy tab's patterns (item 57): default off, the exact bytes
shown, one place that sends, no credentials, withdrawal in one step.

## Clean room

No Google code, text or phrase list was copied. Only the public description of the flow (phrases, record,
upload, transcribe) was used; the Dart sources were not read. So no Apache-2.0 notice is required for
`speechcoach`. If code from the repository is ever reused, keep its licence text, mark the changes and
copy any `NOTICE` content into this repository's `NOTICE`.

## Operator reference

- Recogniser: `GOJIBERRY_VOICESERVER_WHISPER_BINARY` and `_WHISPER_MODEL` (existing); `POST /transcribe`
  now also reads `prompt` (up to 800 bytes, control characters dropped) and `language` (a code such as `de`).
- Research endpoint (optional): receives `POST` with `Content-Type: application/zip` (the export format)
  and `X-Contribution-Token`, and `DELETE <endpoint>/<token>`. Answer 2xx; a 404 on delete means it is gone.
  Keep it behind TLS, store nothing but the package, and delete on request.
