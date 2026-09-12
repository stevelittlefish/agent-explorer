# Agent Explorer

A small, read-only web browser and exporter for local Codex, Claude Code, and pi chats.
Go standard library, server-rendered HTML, CSS, and a little plain JavaScript. No
external dependencies, database, frontend build step, or API keys.

## What does it do?

Agent Explorer turns the chat logs already on your disk into a readable local
website. Choose **an agent → a project → a conversation**, revisit the work, and
export a chat to keep or share. It reads your files without modifying them or
sending them to a service.

- Supports **Codex, Claude Code, and pi**; missing agent folders stay out of the UI.
- Groups consecutive tool calls behind one expandable line.
- Uses a compact dark interface: blue for you, green for the agent.
- Exports standalone **HTML**, readable **text**, or untouched **JSONL**.
- Runs as one executable with templates and assets embedded. No Node, npm,
  database, API keys, or frontend build ceremony.

## Download and run

Linux and Windows **x64 / amd64** builds are produced by
[the Build workflow](https://github.com/stevelittlefish/agent-explorer/actions/workflows/build.yml).
Open a successful run for `main` and download the matching item under **Artifacts**
(GitHub sign-in required). Extract the artifact download, then unpack the enclosed
`.tar.gz` or `.zip`. Each package contains the executable, this README, the license,
and `SHA256SUMS` for checking the executable.

**Linux:**

```sh
tar -xzf agent-explorer-linux-amd64.tar.gz
./agent-explorer
```

**Windows (PowerShell):**

```powershell
Expand-Archive .\agent-explorer-windows-amd64.zip -DestinationPath .\agent-explorer
cd .\agent-explorer
.\agent-explorer.exe
```

Open **http://127.0.0.1:8080**. Keep the terminal open while browsing; press `Ctrl+C`
to stop the server. Use `-addr 127.0.0.1:8087` if port 8080 is occupied.

**Apple builds? No.** We are not volunteering for signed-executable bureaucracy,
notarisation rituals, or a guided tour of somebody else's walled garden.
**Steve Jobs, we just want to run a binary, not apply for planning permission
inside your fruit-shaped kingdom.** Linux and Windows get the automated builds.
Mac users are welcome to try building from source; bring your own ceremonial turtleneck.

## Run from source

Requires Go 1.23 or newer:

```sh
go run .
```

Each screen has its own URL and works without JavaScript. JavaScript adds list
filtering, code copying, and an expand/collapse button for conversation details.

The Phosphor interface uses a dark forest palette, green accents, and a project
chat index beside the conversation. Compact message spacing keeps more of the
conversation in view: your messages are blue and agent replies are green.
Consecutive tool calls and their results fold into a single expandable count
(e.g. "4 tool calls"); outputs do not count as additional calls. The grouping also
works in standalone HTML exports and with JavaScript disabled. On narrow screens the index folds into a
disclosure above the chat. Keyboard navigation and reduced-motion preferences
are supported.

Default data locations:

- Codex: `~/.codex/sessions` and `~/.codex/archived_sessions`.
- Claude Code: `~/.claude/projects`, including nested subagent transcripts.
- pi: `~/.pi/agent/sessions`.

`CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and `PI_CODING_AGENT_DIR` override the default
agent data directories.
You can also specify them explicitly:

```sh
go run . -addr 127.0.0.1:8080 -codex-dir /path/to/.codex -claude-dir /path/to/.claude -pi-dir /path/to/.pi/agent
```

The server binds to loopback by default. It has no authentication; use it locally.
All transcript access is read-only. Agents appear in the home page and navigation
only when their configured data directory (or session directory) exists. An existing
agent directory with no chats shows an empty list. Folder discovery refreshes on
each request; absent agents return 404 if opened directly.

For a custom pi session location, use `-pi-sessions-dir /path/to/sessions` or
`PI_CODING_AGENT_SESSION_DIR`.
Project folders come from recorded working directories; those folders do not need
to exist anymore. Chat lists show most recently modified transcripts first.

## Exports

Each conversation offers:

- **HTML:** a standalone file with embedded styles, escaped message text, and
  expandable tool calls and reasoning. No server or network access needed to read it.
- **Text:** the readable transcript, including tool calls and reasoning.
- **Original JSONL:** the source transcript, unchanged, including original metadata.

Message text and internal whitespace are preserved, with fenced code shown in separate
code blocks. Other Markdown remains readable source text.
Images appear as attachment placeholders; original attachment records remain in
JSONL exports. Provider bookkeeping records and encrypted reasoning are omitted
from readable views. Malformed records are skipped with a warning. Codex response
records take precedence over duplicate event messages; older event-only transcripts
are also supported. pi supports named sessions, messages, thinking, tool calls and
results, shell executions, and saved compaction/branch summaries. pi entries are
shown in file order, including saved branches; original tree metadata remains in
the JSONL export.

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

## Continuous integration

Pushes to `main`, pull requests targeting `main`, and manual workflow runs trigger
native Linux and Windows jobs. Each job runs race-enabled tests and `go vet`,
builds an amd64 executable with CGO disabled, checks that it starts with `-help`,
and uploads a package. CI uses the latest stable Go release. Build artifacts are
kept for 30 days; these are workflow downloads, not automatically published GitHub
Releases. No macOS job sneaks in through the garden gate, Steve Jobs.
