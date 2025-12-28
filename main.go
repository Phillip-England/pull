package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
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
	command := "" // "" (default), "clear", or "write"
	writeTarget := ""

	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}

		if arg == "--append" {
			appendMode = true
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
				if !entry.IsDir() {
					fullPath := filepath.Join(path, entry.Name())
					processFile(fullPath, &sb)
				}
			}
		} else {
			// Pull single file
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
	fmt.Println("  pull <file/dir> ...   Pull content to clipboard")
	fmt.Println("  pull clear            Clear clipboard")
	fmt.Println("  pull write <file>     Write clipboard to file")
	fmt.Println("Flags:")
	fmt.Println("  --append              Append to clipboard instead of overwrite")
}