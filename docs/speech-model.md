# A speech model that adapts to each person

Roadmap item 56. This note answers "which Go route runs a speech model, and how can one base model be adapted
by each person on their own device", and describes what package `speechmodel` already provides so a model can
be plugged in later.

## What to run a model with

| Option | What it is | Fit |
| --- | --- | --- |
| `github.com/orktes/go-torch` | A small pure-Go tensor library from 2017 to 2018. | Not a way to run a modern speech model: it has no attention layers or audio front end, and it is not maintained. |
| `github.com/galeone/tfgo` | Go bindings over the TensorFlow C library (cgo). | Runs TensorFlow SavedModels, but needs the native library (large), does not build to the browser (wasm), and TensorFlow is a poor home for the current speech models, which are published for PyTorch. |
| **ONNX** (`onnxruntime_go` on a server or desktop, `onnxruntime-web` in the browser) | An open model format and a runtime for it. | **Recommended.** A PyTorch model (Whisper and the fine-tuned variants on Hugging Face) exports to ONNX once; the same file runs on the server (cgo, one shared library) and in the browser (WebAssembly or WebGPU, so audio need not leave the device). |
| whisper.cpp behind `cmd/voiceserver` | What the project already uses. | Good as the first recogniser and as the "http" backend; it does not take per-person adapters unless the adapter is merged into the weights ahead of time. |

The recommendation is therefore to keep Go as the **orchestrator** (loading, checking, asking, consent,
aggregation) and to leave the tensor maths to ONNX Runtime, not to port a deep-learning framework to Go.

## The model and the training

- **Base model.** A multilingual speech model that is open to use (for example a Whisper variant), fixed and
  pinned by SHA-256, so every adapter says which model it belongs to.
- **Adaptation.** Low-rank adapters (LoRA) on a few layers: a few megabytes per person instead of a whole model.
  Training needs a framework with autograd, so it is done in PyTorch tooling (or on a server for the person who
  allows it), and the result is exported as an `Adapter`. Training inside Go is not planned.
- **Atypical speech data.** Hugging Face hosts some datasets and models for dysarthric and atypical speech; each
  has its own licence and consent terms, and some are research-only. They are inputs to a decision for the
  maintainers of the model repository, not something this project downloads.
- **Aty-TTS** (the repository named in the request) generates atypical speech from text. It is useful for
  making synthetic training data to improve the base model, not for recognising speech.

## Federated, in the sense this package supports

1. The base model and the shared adapter are published (pinned by digest).
2. On a device, the person's recordings (kept sealed, see `speechcoach`) train a copy of the shared adapter.
3. The device computes `Delta(shared, tuned)`, **clips** its norm and **adds Gaussian noise**
   (`Update.Clip`, `Update.AddNoise`), and shows the person what would be sent.
4. Only with the person's consent (the `speechcoach` Contribution flow) the update is sent; the recordings and
   the transcripts never are.
5. An aggregator averages the updates (`Aggregate`, weighted by examples) and moves the shared adapter by a
   step (`ApplyUpdate`). The new shared adapter is published and the cycle repeats.

What this does **not** give, stated plainly: an update can still reveal something about the voice that made
it, and the package does not compute a formal privacy guarantee (epsilon, delta) from the clip and noise it
applies; the aggregator sees each (clipped, noised) update, because secure aggregation is not built; and a
malicious participant could send a poisoned update, which clipping limits but does not prevent. A deployment
that makes a privacy claim has to measure it and say how.

## What package `speechmodel` is

- `Model`, `Adaptable`, `Trainer`, `Backend`, `Spec`: the contract. `Open` finds the backend, checks the model
  file against its digest and size, and opens it.
- `Adapter` with a checksummed binary format that refuses a changed byte; `Delta`, `Clip`, `AddNoise`,
  `Aggregate`, `ApplyUpdate` for the federated step; all pure Go and tested.
- The `stub` backend (fixed text, wears adapters) and the `http` backend (the `cmd/voiceserver` contract, with
  the address checked by `speechcoach.CheckEndpoint`) so the editor's dictation can be wired and tested now.

## Plan

1. Done: the interface, the adapter format, the federated arithmetic, the stub and the http backend.
2. An `onnx` backend in its own module (so the main module stays free of cgo): `Open` loads an ONNX file the
   Spec pins; `Transcribe` runs the encoder and decoder; `Apply` merges or feeds the adapter.
3. A model repository (it is large enough for its own project): export of the base model to ONNX, the LoRA
   training scripts, the evaluation on atypical speech, the published shared adapter and its aggregation
   service. `speechmodel` would move there with its tests, and gojiberry would keep a thin client.
4. In the editor: the Speech tab (record prompts, dictate with the personal model, correct, contribute),
   as designed in [euphonia-port.md](euphonia-port.md).
