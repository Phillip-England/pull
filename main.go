package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	gitignore "github.com/sabhiram/go-gitignore"
)

const (
	// Safety limit so you don’t accidentally slam your clipboard with a 200MB response.
	maxFetchBytes = 5 << 20 // 5 MiB
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		return
	}

	// 1. Parse Flags and Commands
	var filePaths []string
	appendMode := false
	prependMode := false
	includeIgnored := false
	command := ""
	writeTarget := ""

	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}

		switch arg {
		case "--append":
			appendMode = true
			continue
		case "--prepend":
			prependMode = true
			continue
		case "--includeIgnore":
			includeIgnored = true
			continue
		}

		if command == "" && len(filePaths) == 0 {
			if arg == "clear" {
				command = "clear"
				continue
			}
			if arg == "emit" {
				command = "emit"
				continue
			}
			if arg == "write" {
				command = "write"
				if i+1 < len(args) {
					writeTarget = args[i+1]
					skipNext = true
				}
				continue
			}
			if arg == "href" {
				command = "href"
				continue
			}
		}

		filePaths = append(filePaths, arg)
	}

	// 2. Execute Commands
	switch command {
	case "clear":
		if err := clipboard.WriteAll(""); err != nil {
			fmt.Printf("Error clearing clipboard: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Clipboard cleared.")
		return

	case "emit":
		content, err := clipboard.ReadAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading clipboard: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(content)
		return

	case "write":
		if writeTarget == "" {
			fmt.Println("Error: Missing file path. Usage: pull write ./some_file")
			os.Exit(1)
		}
		content, err := clipboard.ReadAll()
		if err != nil {
			fmt.Printf("Error reading clipboard: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(writeTarget, []byte(content), 0644); err != nil {
			fmt.Printf("Error writing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Clipboard content written to %s\n", writeTarget)
		return

	case "href":
		if len(filePaths) == 0 {
			fmt.Println("Error: Missing URL(s). Usage: pull href <url> [url2 ...]")
			os.Exit(1)
		}

		final, err := buildWithClipboardModes(appendMode, prependMode, func(sb *strings.Builder) error {
			for _, raw := range filePaths {
				u := normalizeURL(raw)
				if err := fetchIntoBuilder(u, sb); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}

		if err := clipboard.WriteAll(final); err != nil {
			fmt.Printf("Error writing to clipboard: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Copied to clipboard!")
		return
	}

	// 3. Default Behavior: Pull files to clipboard
	repoRoot, ign := loadGitIgnoreForCWD()
	_ = repoRoot

	final, err := buildWithClipboardModes(appendMode, prependMode, func(sb *strings.Builder) error {
		for _, startPath := range filePaths {
			err := filepath.WalkDir(startPath, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					fmt.Printf("Skipping %s: %v\n", path, err)
					return nil
				}

				if !includeIgnored && isIgnored(repoRoot, ign, path) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}

				if d.IsDir() {
					return nil
				}

				processFile(path, sb)
				return nil
			})

			if err != nil {
				fmt.Printf("Error walking %s: %v\n", startPath, err)
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if err := clipboard.WriteAll(final); err != nil {
		fmt.Printf("Error writing to clipboard: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Copied to clipboard!")
}

func buildWithClipboardModes(appendMode, prependMode bool, writeNewContent func(sb *strings.Builder) error) (string, error) {
	var sb strings.Builder

	// Append: pre-fill builder with current clipboard
	if appendMode {
		current, err := clipboard.ReadAll()
		if err == nil {
			sb.WriteString(current)
			if current != "" && !strings.HasSuffix(current, "\n") {
				sb.WriteString("\n")
			}
		}
	}

	// Prepend: read clipboard now, attach it at the end later
	var previousContent string
	if prependMode {
		c, err := clipboard.ReadAll()
		if err == nil {
			previousContent = c
		}
	}

	if err := writeNewContent(&sb); err != nil {
		return "", err
	}

	finalContent := sb.String()

	// Apply Prepend Logic: New Content + Old Content
	if prependMode && previousContent != "" {
		if finalContent != "" && !strings.HasSuffix(finalContent, "\n") {
			finalContent += "\n"
		}
		finalContent += previousContent
	}

	return finalContent, nil
}

func fetchIntoBuilder(url string, sb *strings.Builder) error {
	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("href: invalid url %q: %w", url, err)
	}
	req.Header.Set("User-Agent", "pull/1.0 (+clipboard)")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("href: request failed for %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("href: bad status for %q: %s", url, resp.Status)
	}

	body, err := readUpTo(resp.Body, maxFetchBytes)
	if err != nil {
		return fmt.Errorf("href: reading body for %q failed: %w", url, err)
	}

	// Header separator for fetched pages
	sb.WriteString(fmt.Sprintf("href: %s\n", url))
	sb.WriteString(string(body))
	if len(body) > 0 && body[len(body)-1] != '\n' {
		sb.WriteString("\n")
	}
	return nil
}

func readUpTo(r io.Reader, max int64) ([]byte, error) {
	lr := &io.LimitedReader{R: r, N: max + 1}
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("response too large (exceeds maxFetchBytes)")
	}
	return b, nil
}

func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Accept github.com/foo or example.com/path without scheme.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "https://" + s
}

func processFile(path string, sb *strings.Builder) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	sb.WriteString(fmt.Sprintf("file: %s\n", absPath))

	file, err := os.Open(path)
	if err != nil {
		fmt.Printf("Could not open %s: %v\n", path, err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if len(trimmed) == 0 {
			continue
		}

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		sb.WriteString(line)
		sb.WriteString("\n")
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  pull <file/dir> ...           Pull content to clipboard (recursive)")
	fmt.Println("  pull href <url> [url2 ...]    Fetch URL(s) and copy response to clipboard")
	fmt.Println("  pull emit                     Print clipboard content to stdout")
	fmt.Println("  pull clear                    Clear clipboard")
	fmt.Println("  pull write <file>             Write clipboard to file")
	fmt.Println("Flags:")
	fmt.Println("  --append                      Append to clipboard instead of overwrite")
	fmt.Println("  --prepend                     Prepend to clipboard instead of overwrite")
	fmt.Println("  --includeIgnore               Include files that are ignored by .gitignore")
}

// loadGitIgnoreForCWD finds a repo-ish root (nearest parent with .git or .gitignore)
// and loads patterns from <root>/.gitignore if present.
func loadGitIgnoreForCWD() (root string, ign *gitignore.GitIgnore) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil
	}

	root, err = findRepoRoot(cwd)
	if err != nil || root == "" {
		return "", nil
	}

	giPath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(giPath); err == nil {
		if m, err := gitignore.CompileIgnoreFile(giPath); err == nil {
			return root, m
		}
	}
	return root, nil
}

func findRepoRoot(start string) (string, error) {
	start = filepath.Clean(start)
	if es, err := filepath.EvalSymlinks(start); err == nil {
		start = es
	}

	dir := start
	for {
		if existsDir(filepath.Join(dir, ".git")) || existsFile(filepath.Join(dir, ".gitignore")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("repo root not found from %s", start)
}

func existsDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func existsFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isIgnored(repoRoot string, ign *gitignore.GitIgnore, path string) bool {
	if ign == nil || repoRoot == "" {
		return false
	}

	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		absRoot = repoRoot
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	rel = filepath.ToSlash(rel)
	return ign.MatchesPath(rel)
}
