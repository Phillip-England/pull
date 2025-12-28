package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	gitignore "github.com/sabhiram/go-gitignore"
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
	includeIgnored := false // NEW: include ignored files when true
	command := ""           // "" (default), "clear", or "write"
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
		case "--includeIgnore":
			includeIgnored = true
			continue
		}

		// Check for subcommands (only if it's the first non-flag argument)
		if command == "" && len(filePaths) == 0 {
			if arg == "clear" {
				command = "clear"
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
	}

	// 3. Default Behavior: Pull files to clipboard

	// Load .gitignore once (based on current working directory)
	repoRoot, ign := loadGitIgnoreForCWD()
	_ = repoRoot // used by ignore checks

	var sb strings.Builder

	// If appending, read current clipboard first
	if appendMode {
		current, err := clipboard.ReadAll()
		if err == nil {
			sb.WriteString(current)
			if current != "" && !strings.HasSuffix(current, "\n") {
				sb.WriteString("\n")
			}
		}
	}

	for _, path := range filePaths {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Printf("Skipping %s: %v\n", path, err)
			continue
		}

		if info.IsDir() {
			// Pull all files in directory (non-recursive)
			entries, err := os.ReadDir(path)
			if err != nil {
				fmt.Printf("Error reading dir %s: %v\n", path, err)
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				fullPath := filepath.Join(path, entry.Name())

				// Respect .gitignore by default
				if !includeIgnored && isIgnored(repoRoot, ign, fullPath) {
					continue
				}

				processFile(fullPath, &sb)
			}
		} else {
			// Respect .gitignore by default
			if !includeIgnored && isIgnored(repoRoot, ign, path) {
				continue
			}
			processFile(path, &sb)
		}
	}

	finalContent := sb.String()
	if err := clipboard.WriteAll(finalContent); err != nil {
		fmt.Printf("Error writing to clipboard: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Copied to clipboard!")
}

func processFile(path string, sb *strings.Builder) {
	// Resolve absolute path for the header
	absPath, err := filepath.Abs(path)
	if err != nil {
		// Fallback to relative path if absolute fails for some reason
		absPath = path
	}

	// Add the file separator
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

		// Remove empty lines
		if len(trimmed) == 0 {
			continue
		}

		// Remove comments (// and #)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		sb.WriteString(line)
		sb.WriteString("\n")
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  pull <file/dir> ...           Pull content to clipboard (respects .gitignore by default)")
	fmt.Println("  pull clear                    Clear clipboard")
	fmt.Println("  pull write <file>             Write clipboard to file")
	fmt.Println("Flags:")
	fmt.Println("  --append                      Append to clipboard instead of overwrite")
	fmt.Println("  --includeIgnore               Include files that are ignored by .gitignore")
}

// loadGitIgnoreForCWD finds a repo-ish root (nearest parent with .git or .gitignore)
// and loads patterns from <root>/.gitignore if present.
// If not found or not loadable, it returns a nil matcher.
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

	// No .gitignore file (or failed to parse)
	return root, nil
}

// findRepoRoot walks upward from start looking for a directory that contains
// either a ".git" directory or a ".gitignore" file.
// If none found, returns error.
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

// isIgnored returns true if path is matched by the loaded .gitignore matcher.
// If matcher is nil, returns false.
// If path is outside repoRoot, returns false.
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

	// If rel starts with "..", it's outside the root — don't ignore.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	// Gitignore patterns use forward slashes.
	rel = filepath.ToSlash(rel)

	return ign.MatchesPath(rel)
}
