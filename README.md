# Agent Explorer

A small, read-only web browser and exporter for local Codex and Claude Code chats.
Go standard library, server-rendered HTML, CSS, and a little plain JavaScript. No
external dependencies, database, frontend build step, or API keys.

## Run

Requires Go 1.23 or newer.

```sh
go run .
```

Open **http://127.0.0.1:8080**. Choose an agent, then a project folder, then a chat.
Each screen has its own URL and works without JavaScript. JavaScript adds list
filtering, code copying, and an expand/collapse button for conversation details.

The Phosphor interface uses a dark forest palette, green accents, and a project
chat index beside the conversation. On narrow screens the index folds into a
disclosure above the chat. Keyboard navigation and reduced-motion preferences
are supported.

Default data locations:

- Codex: `~/.codex/sessions` and `~/.codex/archived_sessions`.
- Claude Code: `~/.claude/projects`, including nested subagent transcripts.

`CODEX_HOME` and `CLAUDE_CONFIG_DIR` override the default agent data directories.
You can also specify them explicitly:

```sh
go run . -addr 127.0.0.1:8080 -codex-dir /path/to/.codex -claude-dir /path/to/.claude
```

The server binds to loopback by default. It has no authentication; use it locally.
All transcript access is read-only. Missing data directories show an empty list.
Project folders come from recorded working directories; those folders do not need
to exist anymore. Chat lists show most recently modified transcripts first.

## Exports

Each conversation offers:

- **HTML:** a standalone file with embedded styles, escaped message text, and
  expandable tool calls and reasoning. No server or network access needed to read it.
- **Text:** the readable transcript, including tool calls and reasoning.
- **Original JSONL:** the source transcript, unchanged, including original metadata.

Message text and whitespace are preserved, with fenced code shown in separate
code blocks. Other Markdown remains readable source text.
Images appear as attachment placeholders; original attachment records remain in
JSONL exports. Provider bookkeeping records and encrypted reasoning are omitted
from readable views. Malformed records are skipped with a warning. Codex response
records take precedence over duplicate event messages; older event-only transcripts
are also supported.

Chat summaries are cached in memory and refreshed when files change. The selected
conversation is read again on each request. No imported copies or indexes are
written to disk.

## Build and test

```sh
go test ./...
go vet ./...
go build -o agent-explorer .
./agent-explorer
```

Templates and static assets are embedded in the binary. Rebuild after changing them.
