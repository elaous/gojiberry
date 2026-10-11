// Package voice groups what speaks and listens. It has no code of its own; the parts are:
//
//   - voice/speak: text to speech with a local Piper install.
//   - voice/listen: speech to text with a local whisper.cpp install.
//   - voice/server: the HTTP proxy in front of both (cmd/voiceserver).
//   - voice/read: what to say when a document is read aloud (the sentences, in the order and
//     language of the accessible copy); the speaking itself is the browser's.
//
// Braille output is not here: it is its own folder, braille.
package voice
