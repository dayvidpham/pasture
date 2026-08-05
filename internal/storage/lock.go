package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

const (
	initializationLockTimeout = 5 * time.Second
	initializationLockPoll    = 25 * time.Millisecond
)

type initializationLock struct {
	file *os.File
}

func acquireInitializationLock(ctx context.Context, dbPath string) (*initializationLock, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't prepare the directory for the pasture database at %q.", dbPath),
			Why:      "The database parent directory could not be created.",
			Where:    "Preparing database initialization (internal/storage/lock.go in storage.acquireInitializationLock).",
			Impact:   "Pasture did not create a database or start any schema migrations.",
			Fix: fmt.Sprintf("1. Create a writable directory and retry:\n"+
				"     mkdir -p %q\n"+
				"2. Or choose a database path under a writable directory:\n"+
				"     pasture --db <writable-path> init", filepath.Dir(dbPath)),
			Cause: err,
		}
	}
	lockPath := dbPath + ".init.lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, initializationLockError(dbPath, fmt.Sprintf("The initialization lock file %q could not be opened.", lockPath), err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, initializationLockTimeout)
	defer cancel()
	for {
		acquired, err := tryLockFile(file)
		if err != nil {
			_ = file.Close()
			return nil, initializationLockError(dbPath, fmt.Sprintf("The initialization lock file %q could not be locked.", lockPath), err)
		}
		if acquired {
			return &initializationLock{file: file}, nil
		}
		timer := time.NewTimer(initializationLockPoll)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, initializationLockError(dbPath, fmt.Sprintf("Another process kept the initialization lock %q for more than %s.", lockPath, initializationLockTimeout), waitCtx.Err())
		case <-timer.C:
		}
	}
}

func (l *initializationLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return errors.Join(unlockFile(l.file), l.file.Close())
}

func initializationLockError(dbPath, why string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryConnection,
		What:     fmt.Sprintf("Couldn't acquire exclusive initialization access for the pasture database at %q.", dbPath),
		Why:      why,
		Where:    "Serializing database initialization (internal/storage/lock.go in storage.acquireInitializationLock).",
		Impact:   "Pasture did not start schema migrations, so concurrent initializers cannot corrupt or partially migrate the database.",
		Fix:      "Wait for the other pasture init or pastured startup to finish, then retry. If no such process is running, verify the database directory is writable.",
		Cause:    cause,
	}
}
