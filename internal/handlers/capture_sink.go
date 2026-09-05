package handlers

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// CaptureSink records one host payload exactly as the host sent it, so that a
// live session can produce the authentic fixtures the ingress corpus is built
// from. A sink writes bytes and nothing else: no provenance, no redaction, no
// interpretation. Those belong to the clearance procedure that turns a capture
// into a committed fixture.
type CaptureSink interface {
	// Record writes raw to a new file and returns its path. It never
	// overwrites, and it never fails the caller's host: a capture that cannot
	// be written is reported on the warnings stream and returned as an error
	// the caller treats as "not captured", nothing more.
	Record(harness ir.HarnessID, event, hostVersion string, raw []byte) (string, error)
}

// captureNoticePrefix opens the ONE notice a sink prints, on the first payload
// it records. It is printed on the first record and not at construction so
// that it is true when read: a sink that never records has recorded nothing.
const captureNoticePrefix = "pasture: capture mode is recording this session to "

// captureDirectoryEnv names the variable the notices and refusals quote, so an
// operator reading a warning knows which setting produced it. The hook command
// parses that variable; this package only receives its value.
const captureDirectoryEnv = "PASTURE_CAPTURE_DIR"

// captureNumberingCeiling bounds the retry when another process claims the
// same numbered name between the directory scan and the exclusive create. It
// is a ceiling on one invocation's work, not a limit on how many captures a
// directory may hold.
const captureNumberingCeiling = 1000

// captureCoordinate is the shape an event name or host version must have to
// become part of a file name: letters, digits, dot, underscore and hyphen.
// Anything else could name a different path than the one the stem means.
var captureCoordinate = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// DirectoryCaptureSink writes numbered capture files into one existing
// directory outside the repository.
type DirectoryCaptureSink struct {
	dir      string
	warnings io.Writer
	notice   sync.Once
}

// NewDirectoryCaptureSink accepts a capture directory or refuses it. A refusal
// is written to warnings, in the operator's terms, and returned; the caller
// continues with capture off and an unchanged host outcome, because a capture
// setting must never fail the user's session.
//
// The rules, each with the reason that keeps it from being relaxed:
//   - dir must be ABSOLUTE. A relative path would resolve against whatever
//     directory the host started the hook in, so payloads would land where
//     the user did not choose.
//   - dir must be OUTSIDE repoRoot, when repoRoot is not empty. A capture
//     inside the repository can reach a commit before it is cleared; captured
//     payloads reach the repository only through the clearance procedure.
//   - dir must EXIST as a directory. The sink NEVER creates it: a directory
//     pasture creates is one the user did not choose, and captured payloads
//     would land somewhere the user never approved.
//
// warnings is the stream the sink speaks on. It is a parameter, never a
// package variable, so the notice and every refusal can be read by a test
// exactly as an operator would read them.
func NewDirectoryCaptureSink(dir, repoRoot string, warnings io.Writer) (*DirectoryCaptureSink, error) {
	if warnings == nil {
		return nil, fmt.Errorf("cannot build the capture sink because no warnings stream was supplied; " +
			"this happened in NewDirectoryCaptureSink (internal/handlers/capture_sink.go) while the hook was starting; " +
			"the sink speaks only on that stream, so without one every refusal would be silent; pass the hook's standard error")
	}
	refuse := func(what, why, fix string) (*DirectoryCaptureSink, error) {
		err := fmt.Errorf("pasture: %s is %q, which %s, so nothing is captured; %s; "+
			"this happened in NewDirectoryCaptureSink (internal/handlers/capture_sink.go) while the hook was starting, "+
			"before any payload was read; the event is still evaluated and the host is not affected; %s",
			captureDirectoryEnv, dir, what, why, fix)
		fmt.Fprintln(warnings, err.Error())
		return nil, err
	}
	if !filepath.IsAbs(dir) {
		return refuse("is not an absolute path",
			"a relative path would resolve against whatever directory the host started the hook in, and captured payloads would land somewhere you did not choose",
			"set "+captureDirectoryEnv+" to an absolute directory outside this repository, or unset it to turn capture off")
	}
	cleaned := filepath.Clean(dir)
	if repoRoot != "" && isWithin(cleaned, filepath.Clean(repoRoot)) {
		return refuse(fmt.Sprintf("is inside the repository at %q", repoRoot),
			"a capture inside the repository can reach a commit before it is cleared, and captured payloads reach the repository only through the clearance procedure",
			"set "+captureDirectoryEnv+" to an absolute directory outside the repository, or unset it to turn capture off")
	}
	info, err := os.Stat(cleaned)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return refuse("does not exist",
			"pasture does not create it, because a directory pasture creates is one you did not choose, and captured payloads would land somewhere you never approved",
			"create the directory yourself, outside this repository, then start the session again")
	case err != nil:
		return refuse(fmt.Sprintf("could not be inspected (%v)", err),
			"pasture does not create or repair a capture directory, because a directory pasture creates is one you did not choose",
			"make the directory readable by the user running the hook, then start the session again")
	case !info.IsDir():
		return refuse("is not a directory",
			"pasture does not replace it, because a directory pasture creates is one you did not choose, and captured payloads would land somewhere you never approved",
			"point "+captureDirectoryEnv+" at an existing directory outside this repository")
	}
	if repoRoot != "" {
		resolvedDir, dirErr := filepath.EvalSymlinks(cleaned)
		resolvedRoot, rootErr := filepath.EvalSymlinks(filepath.Clean(repoRoot))
		if dirErr == nil && rootErr == nil && isWithin(resolvedDir, resolvedRoot) {
			return refuse(fmt.Sprintf("resolves through a symbolic link to a path inside the repository at %q", repoRoot),
				"a capture inside the repository can reach a commit before it is cleared, and captured payloads reach the repository only through the clearance procedure",
				"set "+captureDirectoryEnv+" to an absolute directory outside the repository, or unset it to turn capture off")
		}
	}
	return &DirectoryCaptureSink{dir: cleaned, warnings: warnings}, nil
}

// isWithin reports whether path is root or lies under root. Both must be clean
// absolute paths.
func isWithin(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// EnclosingRepositoryRoot returns the nearest directory at or above start that
// holds a .git entry, which is a directory for a main checkout and a file for
// a linked worktree. It reports false when no enclosing repository exists, in
// which case no capture directory can be inside one.
func EnclosingRepositoryRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// CaptureStem is the file-name stem of one capture: the harness, the native
// event in snake case and the host version with its dots replaced, joined by
// underscores. It matches the stem the committed fixtures use inside each
// harness's fixture directory, with the harness prefixed because one capture
// directory may hold every harness's sessions. It reports false when the event
// or the version carries a character that is not safe in a file name.
func CaptureStem(harness ir.HarnessID, event, hostVersion string) (string, bool) {
	if harness == "" || !captureCoordinate.MatchString(string(harness)) || !captureCoordinate.MatchString(event) || !captureCoordinate.MatchString(hostVersion) {
		return "", false
	}
	return string(harness) + "_" + snakeCase(event) + "_" + strings.ReplaceAll(hostVersion, ".", "_"), true
}

// snakeCase lowers a native event name into the fixture spelling: an
// underscore before each upper-case letter that follows a lower-case letter or
// a digit, dots to underscores, everything lower case. PreToolUse becomes
// pre_tool_use; tool.execute.before becomes tool_execute_before.
func snakeCase(name string) string {
	var out strings.Builder
	runes := []rune(name)
	for index, r := range runes {
		switch {
		case r == '.':
			out.WriteRune('_')
		case unicode.IsUpper(r):
			if index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1])) {
				out.WriteRune('_')
			}
			out.WriteRune(unicode.ToLower(r))
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Record writes raw to <stem>.<n>.json, where n is one above the highest
// number already present for that stem, created exclusively so that no file
// is ever overwritten. It prints the capture notice on the first payload this
// sink records. A payload that cannot be written is reported on the warnings
// stream and returned as an error; the caller's host outcome is unchanged.
func (s *DirectoryCaptureSink) Record(harness ir.HarnessID, event, hostVersion string, raw []byte) (string, error) {
	warn := func(what, why, fix string) (string, error) {
		err := fmt.Errorf("pasture: %s, so this payload was not captured; %s; "+
			"this happened in DirectoryCaptureSink.Record (internal/handlers/capture_sink.go) while recording event %q of harness %q; "+
			"the event is still evaluated and the host is not affected; %s",
			what, why, event, harness, fix)
		fmt.Fprintln(s.warnings, err.Error())
		return "", err
	}
	stem, ok := CaptureStem(harness, event, hostVersion)
	if !ok {
		return warn(fmt.Sprintf("the event name %q or the host version %q of harness %q carries a character that is not safe in a file name", event, hostVersion, harness),
			"a capture file is named after its coordinates, and an unsafe character could name a different path than the one meant",
			"pass the exact native event name and host version through the hook flags")
	}
	next, err := nextCaptureNumber(s.dir, stem)
	if err != nil {
		return warn(fmt.Sprintf("the capture directory %q could not be listed (%v)", s.dir, err),
			"the next file number is one above the highest already present, so the directory has to be readable",
			fmt.Sprintf("make %q readable by the user running the hook", s.dir))
	}
	for attempt := 0; attempt < captureNumberingCeiling; attempt++ {
		path := filepath.Join(s.dir, fmt.Sprintf("%s.%d.json", stem, next+attempt))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return warn(fmt.Sprintf("the capture %q could not be created (%v)", path, err),
				"a capture is written into the directory you named, and this one refused the write",
				fmt.Sprintf("make %q writable by the user running the hook", s.dir))
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return warn(fmt.Sprintf("the capture %q could not be written completely (%v)", path, errors.Join(writeErr, closeErr)),
				"a partial capture is not authentic evidence, and the file at that path must not be trusted",
				fmt.Sprintf("free space on the filesystem holding %q and remove the partial file", s.dir))
		}
		s.notice.Do(func() { fmt.Fprintln(s.warnings, captureNoticePrefix+s.dir) })
		return path, nil
	}
	return warn(fmt.Sprintf("every numbered name from %s.%d.json for %d attempts already exists", stem, next, captureNumberingCeiling),
		"another writer is claiming the same names faster than this one can, and the sink never overwrites",
		fmt.Sprintf("check what else is writing into %q", s.dir))
}

// nextCaptureNumber returns one above the highest <stem>.<n>.json already in
// dir, or 1 when none exists.
func nextCaptureNumber(dir, stem string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	highest := 0
	prefix := stem + "."
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
		number, err := strconv.Atoi(middle)
		if err != nil || number <= 0 {
			continue
		}
		if number > highest {
			highest = number
		}
	}
	return highest + 1, nil
}
