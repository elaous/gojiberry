# The AI helper: ideas for phase 2

Not designed, not started. This page keeps the ideas so the design can begin from them. The rule that comes first:
**the helper is a mentor and a collaborator, not a ghost-writer.** It suggests, explains and asks; the person
decides, and nothing it writes enters a document unless the person accepts it (the same rule as a tracked change).

## A hub of agents

One place that lists the agents, what each can do, what it needs to read, and where it runs. Each agent is a job with
a few skills:

| Agent | Skills (first list from the owner, then more suggestions) |
| --- | --- |
| **Summarizer** | summarize the open document; summarize several documents chosen from a list of links in the same document; a summary at a chosen length; key points as a list |
| **Critic** | criticize the argument and the structure; find gaps and unsupported claims; play the devil's advocate; say what a reader would ask next |
| **Style coach** | write in the manner of a public-domain author, as general notes on the style (sentence length, diction, rhythm, typical devices), never as imitation of a living writer; adjust tone and reading level; plain-language rewrite |
| **Drafter** | "write a ..." for a kind of document (letter, report, abstract, minutes, README, essay, speech, lesson plan); an outline from notes; a first paragraph to push against |
| **Faithful rewriter** | given a link, read it and write it again in the person's own words while staying faithful to the content, with the passages that were close to the original marked; cites the source |
| **Researcher** | find and check sources for a claim; suggest references in the format of a publication (see Insert > Reference in [status.md](status.md)); mark claims that need a citation |
| **Editor** | consistency of names, dates, numbers and terms; repeated words; long sentences; checks against a glossary |
| **Translator** | translate with a glossary and keep the layout; explain a phrase |
| **Tutor** | explain a passage; make questions about a text; check understanding |
| **Accessibility** | draft alt text and long descriptions (today: a draft made on the device, never automatic), reading-order and plain-language checks |
| **Planner** | break a long document into sections and a schedule; track the writing goals the person sets (from the writing metrics, once they exist) |

## What every agent follows

- **Opt-in and visible.** Off by default. A panel says which agent is running, what it read and where it sent it.
- **Local first.** A model on the person's machine (the grammar client already speaks to an Ollama-compatible server) or
  a server the operator names; a hosted model only when the operator has allowed it and the person has agreed.
- **Confidential documents.** The server cannot read an end-to-end encrypted document, so an agent that needs its text
  runs in the page, and sending the text anywhere is a separate, explicit step that says so.
- **Suggestions arrive as suggestions.** Inserted as a proposal (suggestion mode), or in the side panel, never as a silent
  edit; the result keeps a note of which agent and which model made it.
- **Sources.** An agent that reads a link says which pages it read; what it fetched is not stored beyond the request.
- **Style notes.** The writer-style skill lists the traits of a public-domain author's prose and applies them as advice
  ("shorter sentences, concrete nouns"); it does not ship an author's text.
- **No hidden prompts.** The instructions of each skill can be read in the panel.

## Open questions for the design

1. What is the hub: a server with a list of agents, a protocol (so third parties add agents), or only a list in the page?
2. Model choice and cost: who pays, how a limit is shown, what happens offline.
3. Context: how much of a document an agent may see, how a list of linked documents is read (access rights apply: only what
   the person can open), and how the open document and its comments are summarized for a long document.
4. Evaluation: how a skill is tested (fixtures with fake models, as the grammar client is today) so that a change to a
   prompt shows in a test.
5. The writing metrics ([status.md](status.md), "Ideas from the owner") as a source for the planner and the tutor, under the
   same opt-in.
6. Which parts belong in their own repository ([repo-split.md](repo-split.md)): the hub and its protocol are good candidates.
