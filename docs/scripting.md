# Scripting over the command registry

A person can write (or paste) a small script and run it from the **Scripts** tab. The script does not run in the page: it runs in a sandbox with nothing of the page in it, and the only way out of the sandbox is a message to the host, which answers it according to what the person allowed. The editor's commands are the one thing a script can do, so a command added to the registry is reachable by a script at once, with no list kept by hand.

## What a script sees

`editor` is the only thing a script is given:

| Call | Does | Needs |
|---|---|---|
| `await editor.commands()` | the registry: `{name, title}` of every command (the same list the menus and the key chords are made from) | nothing |
| `await editor.run(name)` | runs a command on the document, as a menu or a chord would | **May run the editor's commands** (on by default) |
| `await editor.text()` | the text of the document | **May read the text of the document** (off by default) |
| `await editor.insert(text)` | puts text at the caret, replacing the selection, as one change the undo history has | **May write text into the document** (off by default) |
| `editor.log(...)` | a line shown under the script | nothing |

### Get and set

With **May read the text of the document** a script can also read more of the document, and with **May change the document** (off by default) it can change it. All of it is checked by the host and nothing of the page is reachable.

| Call | Does | Needs |
|---|---|---|
| `await editor.markdown()` | the document as extended Markdown | read |
| `await editor.setMarkdown(text)` | replaces the whole document with the Markdown (one change the undo history has) | change |
| `await editor.selection()` | `{from, to, text}`: rune offsets and the selected text | read |
| `await editor.setSelection(from, to)` | selects a range | change |
| `await editor.format()` | `{bold, italic, underline, strike, color, background, family, size}` of the first selected character (or the one before the caret) | read |
| `await editor.setFormat({bold, italic, underline, strike, color, background, family, size})` | changes the selection (or what is typed next); what is not given stays; `""` takes a colour, background or family off | change |
| `await editor.styles()`, `await editor.style()` | the names of the paragraph styles, and the style of the paragraph at the caret | read |
| `await editor.setStyle(name)` | applies a style to the selected paragraphs | change |
| `await editor.images()` | `[{index, alt, legend, credit, decorative, width, height}]`, never the picture | read |
| `await editor.setImage(index, {alt, legend, decorative})` | describes a picture | change |
| `await editor.meta()`, `await editor.setMeta({title, language})` | the document's title and language | read, change |

What a script may set is limited: a colour is `#rgb`, `#rrggbb` or a name; a family is letters, digits, spaces and dashes (60 characters); a size is 4 to 400; a description or legend 1000 characters; a title 200; a language a tag like `fr` or `pt-BR`. Changes need edit mode and a person who may edit. Later features that are a small lift are listed in [phase-2.md](phase-2.md) (P2-7).

A refused call rejects with a message the script can read. A script that makes more than 2000 requests, or runs longer than 10 seconds, is stopped; so is one the person stops. A script is not kept: it is a text box, and the page does not store it or run it again by itself.

## The sandbox, and what it does not promise

The sandbox is an iframe with `sandbox="allow-scripts"` (an opaque origin: no cookies, no storage, no access to the page) whose document carries `Content-Security-Policy: default-src 'none'; script-src 'unsafe-inline'` (it may load nothing and connect to nothing). As a second line, `fetch`, `XMLHttpRequest`, sockets, workers, storage and peer connections are removed from its window.

What is *not* promised: a browser's policy and sandbox are strong, not perfect, and a script that may read the text of the document could show it to whoever wrote the script in a line it asks the person to copy. So the page asks for the reading separately, says so, and the documentation says: run scripts you wrote or trust. Commands that change the document do so as the person would; the undo history has them.

## The host

Package `script` is the host side: `Session.Handle` (its get/set requests are `getset.go`: a host that is also a `script.Document` answers them) takes one request as JSON and answers with JSON, checks the permission, caps a request at 64 KiB, a log line at 1000 characters, the text handed over at 4 MiB and the number of requests at 2000. It knows nothing of the page, so it is tested with a stand-in host. The page's part is `scripts.go` in the showcase.

## What is not here

The older plug-in API of opendocs (a Worker that registers its own commands and calls `toggleFormat`, `transformCase` and others) is not ported; scripts here can run the commands and cannot add new ones. Saving scripts, sharing them, running one on a schedule or on an event, and a script adding commands of its own are not built.
