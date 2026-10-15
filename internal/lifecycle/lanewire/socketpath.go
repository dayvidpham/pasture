package lanewire

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// SocketDirectoryName is the subdirectory of the runtime directory (or of the
// store's directory, when there is no runtime directory) that holds the lane
// socket and its election lock.
const SocketDirectoryName = "pasture"

// socketFilePrefix names the socket file itself. The digest in the name makes
// the path a pure function of the store path, so two daemons configured for
// different stores never collide and a client that knows only the store path
// finds the right socket.
const socketFilePrefix = "lane-"

// digestLength is how many hex characters of the store-path digest name the
// socket. Twelve hex characters is the design's choice: enough that two
// distinct store paths collide only by accident, and short enough to stay well
// inside the 108-byte sun_path bound even under a long runtime directory.
const digestLength = 12

// SocketPath returns the deterministic Unix socket path for one store path.
//
// WHERE IT LIVES, AND WHY. The runtime directory is where a per-user,
// per-boot socket belongs and is already 0700 by the operating system's own
// rule; a store whose directory is not writable still gets a lane. When
// XDG_RUNTIME_DIR is unset the socket sits beside the database instead, which
// is the fallback the design names.
//
// It is a pure function of the store path in BOTH cases, so the daemon and
// every hook derive the same path without communicating.
func SocketPath(dbPath string) string {
	digest := sha256.Sum256([]byte(dbPath))
	name := socketFilePrefix + hex.EncodeToString(digest[:])[:digestLength] + ".sock"
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, SocketDirectoryName, name)
	}
	directory := filepath.Dir(dbPath)
	if directory == "" || directory == "." {
		return name
	}
	return filepath.Join(directory, name)
}

// LockPath returns the exclusive lock file the lane election uses. It sits
// beside the socket and is never removed while a holder lives, so a stale
// socket can be reclaimed through the lock rather than by unlinking on connect.
func LockPath(socketPath string) string { return socketPath + ".lock" }

// SocketDir returns the directory the socket and its lock live in.
func SocketDir(socketPath string) string { return filepath.Dir(socketPath) }
