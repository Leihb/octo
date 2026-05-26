package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
)

// ReadFileMaxLines caps how many lines a single read_file call returns.
// Past this, the caller must paginate via offset/limit. Matches the cap
// Claude Code's Read tool uses (helps keep LLM context sane).
const ReadFileMaxLines = 2000

// ReadFileTool reads a UTF-8 text file, optionally a window of it, and
// returns its content with each line prefixed by its 1-based line number
// (the `cat -n` format the LLM is already familiar with).
type ReadFileTool struct{}

func (ReadFileTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "read_file",
		Description: "Read a UTF-8 text file and return its content with cat -n-style " +
			"line numbers. Up to 2000 lines per call — use offset/limit to paginate. " +
			"Absolute paths are preferred; relative paths resolve against the current " +
			"working directory. Refuses binary extensions (executables, archives, " +
			"images, PDFs, DBs) and blocking device files (/dev/random, /dev/tty etc).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path (absolute preferred).",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "1-based line number to start from. Defaults to 1 (beginning of file).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum lines to return. Defaults to 2000.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (ReadFileTool) Execute(_ context.Context, _ string, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("read_file: path is required")
	}
	abs, err := resolvePath(path)
	if err != nil {
		return "", err
	}

	// Refuse binaries by extension. Cheaper than mime-sniffing and stops
	// the LLM from blowing its context on a multi-MB .exe / .png. PDFs and
	// images would need separate tooling (image embedding, PDF text
	// extraction) — out of scope for read_file.
	if reason := isLikelyBinaryPath(abs); reason != "" {
		return "", fmt.Errorf("read_file: refusing to read %s — %s", path, reason)
	}

	// Refuse device files that would block forever or generate infinite
	// output. /dev/null is permitted because it just returns EOF immediately.
	if isBlockedDevicePath(abs) {
		return "", fmt.Errorf("read_file: refusing to read %s — device file would block or stream indefinitely", path)
	}

	offset := intArg(input, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intArg(input, "limit", ReadFileMaxLines)
	if limit < 1 || limit > ReadFileMaxLines {
		limit = ReadFileMaxLines
	}

	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("read_file: open %q: %w", path, err)
	}
	defer f.Close()

	// Larger initial buffer than bufio.Scanner's 64KiB default so long
	// lines (minified JS, base64 blobs) don't trip MaxScanTokenSize.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var (
		out      strings.Builder
		lineNum  int
		returned int
	)
	for scanner.Scan() {
		lineNum++
		if lineNum < offset {
			continue
		}
		if returned >= limit {
			break
		}
		// Width-6 line number column matches `cat -n` so a 100k-line file
		// still aligns. Tab after the column keeps Markdown/code blocks
		// rendering correctly in UIs.
		fmt.Fprintf(&out, "%6d\t%s\n", lineNum, scanner.Text())
		returned++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read_file: scan %q: %w", path, err)
	}

	if returned == 0 && lineNum > 0 {
		return "", fmt.Errorf("read_file: offset %d is past end of file (only %d lines)", offset, lineNum)
	}
	if returned == 0 {
		return "(empty file)", nil
	}
	return out.String(), nil
}

// resolvePath normalises path → absolute, expanding "~" and cleaning.
func resolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path), nil
}

// intArg pulls an int from input, accepting either int or float64
// (JSON numbers arrive as float64 from encoding/json).
func intArg(input map[string]any, key string, dflt int) int {
	switch v := input[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return dflt
}

// binaryExtensions is the set of file extensions read_file refuses outright.
// The list focuses on formats that are almost never useful as text in an
// LLM context (executables, archives, media, compiled artefacts). PDFs and
// images are *also* binary but excluded from this map — they're plausibly
// useful via separate extraction tools we may add later, and the user-facing
// error message becomes more accurate ("use a PDF tool") than the generic
// "binary file" wording. For now those still hit this list as a catch-all.
var binaryExtensions = map[string]string{
	// Executables / libraries
	".exe": "Windows executable", ".dll": "Windows library",
	".so": "Linux shared object", ".dylib": "macOS dynamic library",
	".o": "object file", ".a": "static archive", ".lib": "library",
	".class": "Java class file", ".jar": "Java archive",
	".pyc": "compiled Python", ".pyo": "compiled Python",
	".wasm": "WebAssembly",
	// Archives
	".zip": "ZIP archive", ".tar": "tar archive", ".gz": "gzip archive",
	".bz2": "bzip2 archive", ".xz": "xz archive", ".7z": "7-zip archive",
	".rar": "RAR archive",
	// Images
	".png": "PNG image", ".jpg": "JPEG image", ".jpeg": "JPEG image",
	".gif": "GIF image", ".bmp": "BMP image", ".webp": "WebP image",
	".ico": "icon", ".tiff": "TIFF image", ".heic": "HEIC image",
	// Audio / video
	".mp3": "MP3 audio", ".mp4": "MP4 video", ".mov": "video",
	".avi": "video", ".mkv": "video", ".webm": "video",
	".ogg": "audio", ".wav": "audio", ".flac": "audio", ".m4a": "audio",
	// Fonts
	".ttf": "font", ".otf": "font", ".woff": "web font", ".woff2": "web font",
	// Documents (non-text)
	".pdf": "PDF document (use a PDF-aware tool)",
	".doc": "Word document", ".docx": "Word document",
	".xls": "Excel spreadsheet", ".xlsx": "Excel spreadsheet",
	".ppt": "PowerPoint", ".pptx": "PowerPoint",
	// Databases / binary data
	".db": "database", ".sqlite": "SQLite database", ".sqlite3": "SQLite database",
	".dat": "binary data",
}

// isLikelyBinaryPath returns a human-readable reason if the path's
// extension is on the binary-refusal list, otherwise "".
func isLikelyBinaryPath(absPath string) string {
	ext := strings.ToLower(filepath.Ext(absPath))
	if reason, ok := binaryExtensions[ext]; ok {
		return reason + " (read_file only handles text)"
	}
	return ""
}

// blockedDevicePaths are paths whose read would block the agent forever
// or generate effectively-infinite output. /dev/null is intentionally
// absent — reading it just returns EOF and is safe.
var blockedDevicePaths = map[string]struct{}{
	"/dev/random":  {},
	"/dev/urandom": {},
	"/dev/zero":    {},
	"/dev/full":    {},
	"/dev/tty":     {},
	"/dev/stdin":   {},
	"/dev/stdout":  {},
	"/dev/stderr":  {},
}

// isBlockedDevicePath reports whether the absolute path names a device
// file that would block or stream indefinitely. The list is exact-match
// only; /dev/pts/* and similar slot-allocated TTYs aren't covered here
// because the LLM has no business reading them in the first place and
// they'd surface via "would block" rather than "infinite output".
func isBlockedDevicePath(absPath string) bool {
	_, blocked := blockedDevicePaths[absPath]
	return blocked
}
