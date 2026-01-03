package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	gitignore "github.com/sabhiram/go-gitignore"
)

const (
	maxFetchBytes   = 5 << 20 // 5 MiB (href + github file fetch safety limit)
	githubAPIRoot   = "https://api.github.com"
	githubAPIVer    = "2022-11-28"
	githubUserAgent = "pull/1.0 (+clipboard)"
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

	// Default mode: pull local files/dirs AND/OR GitHub paths.
	repoRoot, ign := loadGitIgnoreForCWD()
	_ = repoRoot

	final, err := buildWithClipboardModes(appendMode, prependMode, func(sb *strings.Builder) error {
		for _, startPath := range filePaths {
			// GitHub mode
			if looksLikeGitHubSpec(startPath) {
				spec, err := parseGitHubSpec(startPath)
				if err != nil {
					return err
				}
				if err := fetchGitHubSpecIntoBuilder(spec, sb); err != nil {
					return err
				}
				continue
			}

			// Local filesystem mode
			err := filepath.WalkDir(startPath, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					fmt.Printf("Skipping %s: %v\n", p, err)
					return nil
				}
				if !includeIgnored && isIgnored(repoRoot, ign, p) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					return nil
				}
				processFile(p, sb)
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

	if appendMode {
		current, err := clipboard.ReadAll()
		if err == nil {
			sb.WriteString(current)
			if current != "" && !strings.HasSuffix(current, "\n") {
				sb.WriteString("\n")
			}
		}
	}

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
	if prependMode && previousContent != "" {
		if finalContent != "" && !strings.HasSuffix(finalContent, "\n") {
			finalContent += "\n"
		}
		finalContent += previousContent
	}

	return finalContent, nil
}

func fetchIntoBuilder(u string, sb *strings.Builder) error {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return fmt.Errorf("href: invalid url %q: %w", u, err)
	}
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("href: request failed for %q: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("href: bad status for %q: %s", u, resp.Status)
	}

	body, err := readUpTo(resp.Body, maxFetchBytes)
	if err != nil {
		return fmt.Errorf("href: reading body for %q failed: %w", u, err)
	}

	sb.WriteString(fmt.Sprintf("href: %s\n", u))
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
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "https://" + s
}

func processFile(p string, sb *strings.Builder) {
	absPath, err := filepath.Abs(p)
	if err != nil {
		absPath = p
	}

	sb.WriteString(fmt.Sprintf("file: %s\n", absPath))

	file, err := os.Open(p)
	if err != nil {
		fmt.Printf("Could not open %s: %v\n", p, err)
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
	fmt.Println("  pull <file/dir> ...                         Pull content to clipboard (recursive)")
	fmt.Println("  pull github.com/<owner>/<repo>[@ref][/path] Pull GitHub repo/path to clipboard (recursive)")
	fmt.Println("  pull https://github.com/<owner>/<repo>/tree/<ref>/<path>   Pull GitHub tree URL (recursive)")
	fmt.Println("  pull https://github.com/<owner>/<repo>/blob/<ref>/<path>   Pull GitHub blob URL (single file)")
	fmt.Println("  pull href <url> [url2 ...]                  Fetch URL(s) and copy response to clipboard")
	fmt.Println("  pull emit                                   Print clipboard content to stdout")
	fmt.Println("  pull clear                                  Clear clipboard")
	fmt.Println("  pull write <file>                           Write clipboard to file")
	fmt.Println("Flags:")
	fmt.Println("  --append                                    Append to clipboard instead of overwrite")
	fmt.Println("  --prepend                                   Prepend to clipboard instead of overwrite")
	fmt.Println("  --includeIgnore                             Include files that are ignored by .gitignore")
	fmt.Println("")
	fmt.Println("GitHub auth (recommended):")
	fmt.Println("  export GITHUB_TOKEN=ghp_...   (or fine-grained token with repo read access)")
}

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

func isIgnored(repoRoot string, ign *gitignore.GitIgnore, p string) bool {
	if ign == nil || repoRoot == "" {
		return false
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		absRoot = repoRoot
	}
	absPath, err := filepath.Abs(p)
	if err != nil {
		absPath = p
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

//
// -------------------------- GitHub support --------------------------
//

type gitHubSpec struct {
	Owner string
	Repo  string
	Ref   string // optional
	Path  string // optional path inside repo, no leading slash, POSIX style
	// Original input for labeling
	Label string
}

func looksLikeGitHubSpec(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "github.com/") {
		return true
	}
	if strings.HasPrefix(s, "https://github.com/") || strings.HasPrefix(s, "http://github.com/") {
		return true
	}
	return false
}

func parseGitHubSpec(raw string) (gitHubSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return gitHubSpec{}, errors.New("github: empty spec")
	}

	// Normalize to URL so we can parse reliably.
	u := raw
	if strings.HasPrefix(u, "github.com/") {
		u = "https://" + u
	}

	pu, err := url.Parse(u)
	if err != nil {
		return gitHubSpec{}, fmt.Errorf("github: invalid url %q: %w", raw, err)
	}
	if pu.Host != "github.com" && pu.Host != "www.github.com" {
		return gitHubSpec{}, fmt.Errorf("github: expected github.com host, got %q", pu.Host)
	}

	// Path segments:
	// /owner/repo
	// /owner/repo/tree/ref/path...
	// /owner/repo/blob/ref/path...
	segs := splitPathKeepOrder(pu.Path)
	if len(segs) < 2 {
		return gitHubSpec{}, fmt.Errorf("github: expected github.com/<owner>/<repo>, got %q", raw)
	}

	owner := segs[0]
	repo := segs[1]
	ref := ""
	subPath := ""

	if len(segs) >= 4 && (segs[2] == "tree" || segs[2] == "blob") {
		ref = segs[3]
		if len(segs) > 4 {
			subPath = strings.Join(segs[4:], "/")
		}
	} else if len(segs) > 2 {
		// /owner/repo/<path...> (no explicit ref)
		subPath = strings.Join(segs[2:], "/")
	}

	// Support @ref after repo in shorthand:
	// github.com/owner/repo@main/path
	// also allow repo@ref with no further path.
	if at := strings.Index(repo, "@"); at != -1 {
		ref = repo[at+1:]
		repo = repo[:at]
	}

	// Also allow @ref at the start of subPath: /owner/repo@ref/path is covered above,
	// but for safety if user does github.com/owner/repo/@ref/path (rare) we won’t support.

	spec := gitHubSpec{
		Owner: owner,
		Repo:  repo,
		Ref:   ref,
		Path:  strings.TrimPrefix(subPath, "/"),
		Label: raw,
	}
	return spec, nil
}

func splitPathKeepOrder(p string) []string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

type ghClient struct {
	http  *http.Client
	token string
}

func newGHClient() *ghClient {
	return &ghClient{
		http:  &http.Client{Timeout: 20 * time.Second},
		token: strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
	}
}

func (c *ghClient) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", githubUserAgent)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVer)

	// Default accept for JSON unless caller sets something else.
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	return c.http.Do(req)
}

func fetchGitHubSpecIntoBuilder(spec gitHubSpec, sb *strings.Builder) error {
	c := newGHClient()

	// Label the operation (useful when mixing local + github).
	sb.WriteString(fmt.Sprintf("github: %s\n", spec.Label))

	// If user provided a blob URL path but no file extension… still handled by contents API.
	// We’ll resolve the spec target via contents API and recurse if it’s a directory.
	return c.walkContents(spec.Owner, spec.Repo, spec.Ref, spec.Path, sb)
}

type ghContentItem struct {
	Type        string `json:"type"` // "file" | "dir" | ...
	Name        string `json:"name"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
}

func (c *ghClient) walkContents(owner, repo, ref, repoPath string, sb *strings.Builder) error {
	// Query /repos/{owner}/{repo}/contents/{path}?ref=
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents", githubAPIRoot, owner, repo)
	if repoPath != "" {
		// Must be URL path-escaped in a path-safe way:
		// We’ll join with POSIX separators and escape segments.
		endpoint = endpoint + "/" + escapeGitHubPath(repoPath)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	if ref != "" {
		q := u.Query()
		q.Set("ref", ref)
		u.RawQuery = q.Encode()
	}

	// First try JSON (could be file object or array for dir listing).
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("github: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := readUpTo(resp.Body, maxFetchBytes)
	if err != nil {
		return fmt.Errorf("github: response too large at %s: %w", u.String(), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Attempt to surface GitHub’s message if present.
		msg := extractGitHubMessage(body)
		if msg != "" {
			return fmt.Errorf("github: %s (%s)", msg, resp.Status)
		}
		return fmt.Errorf("github: bad status %s", resp.Status)
	}

	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil
	}

	// If it starts with '[' it's a directory listing.
	if trim[0] == '[' {
		var items []ghContentItem
		if err := json.Unmarshal(trim, &items); err != nil {
			return fmt.Errorf("github: decode dir listing failed: %w", err)
		}
		// Recurse
		for _, it := range items {
			switch it.Type {
			case "dir":
				if err := c.walkContents(owner, repo, ref, it.Path, sb); err != nil {
					return err
				}
			case "file":
				if err := c.fetchFileRaw(owner, repo, ref, it.Path, sb); err != nil {
					return err
				}
			default:
				// Skip symlinks/submodules/etc for now; could be added later.
				continue
			}
		}
		return nil
	}

	// Otherwise it's a single object (file or dir metadata).
	var single ghContentItem
	if err := json.Unmarshal(trim, &single); err != nil {
		return fmt.Errorf("github: decode content failed: %w", err)
	}

	switch single.Type {
	case "dir":
		return c.walkContents(owner, repo, ref, single.Path, sb)
	case "file":
		return c.fetchFileRaw(owner, repo, ref, single.Path, sb)
	default:
		return fmt.Errorf("github: unsupported content type %q at %s/%s:%s", single.Type, owner, repo, repoPath)
	}
}

func (c *ghClient) fetchFileRaw(owner, repo, ref, repoPath string, sb *strings.Builder) error {
	// Use the contents endpoint with the "raw" media type so we get file bytes directly.
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s", githubAPIRoot, owner, repo, escapeGitHubPath(repoPath))

	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	if ref != "" {
		q := u.Query()
		q.Set("ref", ref)
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.raw+json")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("github: raw fetch failed for %s: %w", repoPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := readUpTo(resp.Body, maxFetchBytes)
		msg := extractGitHubMessage(b)
		if msg != "" {
			return fmt.Errorf("github: %s (%s)", msg, resp.Status)
		}
		return fmt.Errorf("github: raw fetch bad status for %s: %s", repoPath, resp.Status)
	}

	b, err := readUpTo(resp.Body, maxFetchBytes)
	if err != nil {
		return fmt.Errorf("github: file too large %s (>%d bytes): %w", repoPath, maxFetchBytes, err)
	}

	// Label similarly to local:
	// file: github.com/owner/repo@ref/path
	label := fmt.Sprintf("github.com/%s/%s", owner, repo)
	if ref != "" {
		label += "@" + ref
	}
	label = label + "/" + repoPath

	sb.WriteString(fmt.Sprintf("file: %s\n", label))

	// Keep your existing behavior: skip empty lines + comment-only lines.
	scanner := bufio.NewScanner(bytes.NewReader(b))
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
	return nil
}

func escapeGitHubPath(p string) string {
	// Escape each segment but keep slashes.
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return ""
	}
	segs := strings.Split(p, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return path.Join(segs...)
}

func extractGitHubMessage(body []byte) string {
	// GitHub error bodies often look like: {"message":"...","documentation_url":"..."}
	var v struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &v)
	return strings.TrimSpace(v.Message)
}
