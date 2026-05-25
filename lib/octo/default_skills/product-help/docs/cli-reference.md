# CLI Reference

## Commands

### `octo agent` (default)

Run an AI agent in interactive mode.

```bash
octo agent [OPTIONS]
```

**Options:**

| Option | Description |
|---|---|
| `--mode=MODE` | Permission mode: `auto_approve`, `confirm_safes`, `confirm_all` (default: `confirm_safes`) |
| `--theme=THEME` | UI theme: `hacker`, `minimal` (default: `hacker`) |
| `-v, --verbose` | Show detailed output |
| `--path=PATH` | Project directory path (default: current directory) |
| `-c, --continue` | Continue most recent session |
| `-l, --list` | List recent sessions |
| `-a, --attach=N` | Attach to session by number or ID prefix |
| `--json` | Output NDJSON to stdout |
| `-m, --message=MSG` | Run non-interactively and exit |
| `-f, --file=PATH` | Attach file(s) (use with `-m`) |
| `--agent=PROFILE` | Agent profile: `coding`, `general` (default: `coding`) |
| `--model=MODEL` | Override the model to use |
| `--max-turns=N` | Per-task turn budget; aborts when the LLM keeps tool-looping past this number (default: `30`, `0` disables) |
| `--max-cost=N` | Session USD budget; aborts when cumulative cost exceeds this number (unlimited by default) |

**Examples:**

```bash
# Interactive mode
octo

# Auto-approve all tools
octo --mode=auto_approve

# Continue previous session
octo -c

# One-shot task
octo -m "write a hello world script"

# Attach files
octo -m "review this code" -f src/main.rb
```

### `octo server`

Start the Web UI server.

```bash
octo server [OPTIONS]
```

| Option | Description |
|---|---|
| `-b, --host=HOST` | Bind host (default: `127.0.0.1`) |
| `-p, --port=N` | Listen port (default: `8888`) |
| `--no-compression` | Disable message compression |
| `--no-memory` | Disable automatic memory updates |
| `--no-caching` | Disable prompt caching |
| `--no-skill-evolution` | Disable automatic skill evolution |

### `octo tree`

Print a tree of all available commands.

```bash
octo tree
```

## Interactive Commands

During an agent session, you can use:

| Command | Description |
|---|---|
| `/config` | Open configuration editor |
| `/cost` | Show this session's token totals and estimated USD cost |
| `/` | List and invoke skills |
| `exit` or `quit` | End the session |
