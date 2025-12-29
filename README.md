# pull

`pull` is a clipboard-first CLI tool for collecting content from files, directories, or URLs and moving it instantly into your clipboard (or stdout).

It is designed for:
- AI / LLM workflows
- Static-site tooling
- Rapid iteration and inspection
- Fast “grab → paste → think” development loops

> Think: `cat`, `curl`, and your clipboard — deliberately opinionated.

---

## Features

- 📋 Clipboard-first by default
- 📁 Recursively pull files and directories
- 🌐 Fetch web pages via simple HTTP (`href`)
- 🚫 Honors `.gitignore` automatically
- ➕ Append or prepend instead of overwriting
- 🔄 Pipe clipboard content to stdout
- ✍️ Write clipboard contents directly to a file

---

## Installation

### Using Go

```bash
go install github.com/phillip-england/pull@latest
```

### From source

```bash
git clone https://github.com/phillip-england/pull
cd pull
go build
```

---

## Usage

### Pull files or directories into the clipboard

```bash
pull main.go
pull src/
pull src cmd internal
```

Behavior:
- Recurses through directories
- Removes empty lines and comments
- Adds file headers for clarity

---

### Respecting `.gitignore`

By default, ignored files (such as `node_modules`, `dist`, etc.) are skipped.

To include them:

```bash
pull --includeIgnore src/
```

---

### Fetch web pages (`href`)

Fetch one or more URLs and copy the response body into the clipboard.

```bash
pull href github.com/phillip-england
pull href https://example.com
```

Notes:
- Automatically prepends `https://` if missing
- Performs a simple `GET` request
- **Non-2xx HTTP responses return an error**
- Response size is capped for safety

Multiple URLs:

```bash
pull href github.com/phillip-england example.com docs.bun.sh
```

---

### Append or prepend instead of overwrite

Append new content to what’s already in the clipboard:

```bash
pull --append src/
pull --append href example.com
```

Prepend new content:

```bash
pull --prepend main.go
```

---

### Emit clipboard to stdout

Useful for piping, inspection, or transformation:

```bash
pull emit
pull emit | sed 's/foo/bar/'
```

---

### Write clipboard contents to a file

```bash
pull write output.txt
```

Writes the clipboard contents exactly as-is.

---

## Examples

Pull source code and a webpage into the same clipboard payload:

```bash
pull src/
pull --append href example.com
```

Use `pull` to quickly assemble AI prompts:

```bash
pull src/handlers api/routes
pull emit | llm
```

---

## Philosophy

`pull` is intentionally simple:
- No config files
- No background processes
- No hidden state

It does one thing well: **move useful content into your clipboard, fast**.

---

## License

MIT License © Phillip England
