# pull — copy files to clipboard (and write clipboard back)

`pull` is a tiny CLI that:

- **Copies one or more files (or all files in a directory) to your clipboard**
- **Optionally appends** to what’s already on your clipboard (`--append`)
- **Clears** your clipboard (`pull clear`)
- **Writes** your clipboard contents into a file (`pull write <path>`)
-- **Ignores** files in your `.gitignore`

It’s designed for quick “grab file content → paste somewhere” workflows.

---

## Install (Go)

You can install with `go install` (Go 1.18+):

```bash
go install github.com/phillip-england/pull@latest
```

Replace `<MODULE_PATH>` with the module path for this repository (for example: `github.com/yourname/pull`).

After installing, make sure your Go bin directory is in your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Verify:

```bash
pull
```

> **macOS note:** Clipboard access may require permission prompts depending on how/where you run the binary (Terminal, iTerm, etc.). If clipboard operations fail, check **System Settings → Privacy & Security → Clipboard**.

---

## Usage

### Copy files/directories to clipboard

```bash
pull <file_or_dir> [more_files_or_dirs...]
```

- If you pass a **file**, it’s processed line-by-line.
- If you pass a **directory**, it processes **all files in that directory (non-recursive)**.
- Output is written to the clipboard and prefixed with a header per file:

```
file: /absolute/path/to/file
<filtered content...>
```

### Append instead of overwrite

```bash
pull --append <file_or_dir> ...
```

### Clear clipboard

```bash
pull clear
```

### Write clipboard contents to a file

```bash
pull write ./some_file.txt
```

---

## Filtering behavior

When copying to the clipboard, `pull`:

- **Removes empty lines**
- **Removes comment-only lines** that begin with `//` or `#` (after trimming whitespace)
- Preserves other lines exactly (including indentation)

This is useful for copying “just the meaningful content” out of source files.

---

## Examples

Copy two files:

```bash
pull ./main.go ./go.mod
```

Copy all files in a directory (non-recursive):

```bash
pull ./pkg
```

Append a directory’s content to whatever is already on your clipboard:

```bash
pull --append ./pkg
pull --prepend ./pkg
```

Clear clipboard:

```bash
pull clear
```

Write clipboard to a new file:

```bash
pull write ./notes.txt
```

---

## Exit codes / behavior

- Skips missing paths and prints a message like:
  - `Skipping <path>: <error>`
- Exits with code `1` on clipboard read/write failures or write-to-file failures.
- Prints `Copied to clipboard!` on success.

---

## Dependencies

This tool uses the cross-platform clipboard library:

- `github.com/atotto/clipboard`

---

## License

Add your preferred license (MIT, Apache-2.0, etc.) to the repository root.
