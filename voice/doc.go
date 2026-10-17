// Package voice groups what speaks and listens. It has no code of its own; the parts are:
//
//   - voice/speak: text to speech with a local Piper install.
//   - voice/listen: speech to text with a local whisper.cpp install.
//   - voice/server: the HTTP proxy in front of both (cmd/voiceserver).
//   - speechcoach: recording sessions, a personal dataset, a vocabulary prompt and a correction memory for
//     people whose speech standard recognisers handle badly (roadmap 56); voice/server takes its prompt.
//   - voice/read: what to say when a document is read aloud (the sentences, in the order and
//     language of the accessible copy), the port of the engines that say it (read.Engine, with a Player over it), and
//     voice/read/piper, the adapter for downloaded Piper voices that run in the page; the other adapter, for the
//     browser's own voices, is in the page.
//
// Braille output is not here: it is its own folder, braille.
package voice
