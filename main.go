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
	prependMode := false // [NEW]
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
		case "--prepend": // [NEW]
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
	repoRoot, ign := loadGitIgnoreForCWD()
	_ = repoRoot

	var sb strings.Builder

	// [EXISTING] Handle Append: Pre-fill builder with current clipboard
	if appendMode {
		current, err := clipboard.ReadAll()
		if err == nil {
			sb.WriteString(current)
			if current != "" && !strings.HasSuffix(current, "\n") {
				sb.WriteString("\n")
			}
		}
	}

	// [NEW] Handle Prepend: Read clipboard now, attach it at the end later
	var previousContent string
	if prependMode {
		c, err := clipboard.ReadAll()
		if err == nil {
			previousContent = c
		}
	}

	for _, startPath := range filePaths {
		// Use WalkDir for recursion
		err := filepath.WalkDir(startPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				// If we can't access a specific path, print error but continue walking others
				fmt.Printf("Skipping %s: %v\n", path, err)
				return nil
			}

			// Check .gitignore
			if !includeIgnored && isIgnored(repoRoot, ign, path) {
				if d.IsDir() {
					// Optimization: If the directory itself is ignored (e.g. node_modules),
					// skip the entire directory tree.
					return filepath.SkipDir
				}
				return nil
			}

			// If it's a directory, we just continue (we only process files)
			if d.IsDir() {
				return nil
			}

			// Process the file
			processFile(path, &sb)
			return nil
		})

		if err != nil {
			fmt.Printf("Error walking %s: %v\n", startPath, err)
		}
	}

	finalContent := sb.String()

	// [NEW] Apply Prepend Logic
	// Result = New Content (sb) + Old Content (previousContent)
	if prependMode && previousContent != "" {
		if finalContent != "" && !strings.HasSuffix(finalContent, "\n") {
			finalContent += "\n"
		}
		finalContent += previousContent
	}

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
	fmt.Println("  pull <file/dir> ...           Pull content to clipboard (recursive)")
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