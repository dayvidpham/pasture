//go:build pasture_experiment

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/writelane"
)

type experimentBudgetInput struct {
	BatchRecords      *int   `json:"batch_records"`
	BatchBytes        *int64 `json:"batch_bytes"`
	QueueRecords      *int   `json:"queue_records"`
	QueueBytes        *int64 `json:"queue_bytes"`
	FlushMilliseconds *int   `json:"flush_ms"`
}

func experimentPathInside(root, path string) error {
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("experiment sandbox must exist: %w", err)
	}
	if !filepath.IsAbs(root) || !strings.HasPrefix(physicalRoot, "/tmp/opencode/") {
		return fmt.Errorf("experiment sandbox must be an absolute private directory below /tmp/opencode")
	}
	info, err := os.Stat(physicalRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("experiment sandbox must be an existing private directory (0700)")
	}
	physicalPath, err := experimentCanonicalPath(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(physicalRoot, physicalPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return fmt.Errorf("experiment path must stay strictly inside the private sandbox")
	}
	return nil
}

func experimentCanonicalPath(path string) (string, error) {
	physicalPath, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		if _, leafErr := os.Lstat(path); !os.IsNotExist(leafErr) {
			return "", fmt.Errorf("experiment output leaf already exists or is a dangling symlink; choose a fresh output file")
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
		if parentErr != nil {
			return "", parentErr
		}
		physicalPath, err = filepath.Join(parent, filepath.Base(path)), nil
	}
	if err != nil {
		return "", err
	}
	return filepath.Abs(physicalPath)
}

func laneExperimentBudget(base writelane.WriteLaneBudget, db string) (writelane.WriteLaneBudget, error) {
	raw, metrics := os.Getenv("PASTURE_EXPERIMENT_LANE_BUDGET"), os.Getenv("PASTURE_EXPERIMENT_LANE_METRICS")
	if raw == "" && metrics == "" {
		return base, nil
	}
	root := os.Getenv("PASTURE_EXPERIMENT_SANDBOX")
	if err := experimentPathInside(root, db); err != nil {
		return base, fmt.Errorf("refusing experimental lane configuration for database: %w", err)
	}
	if metrics != "" {
		if err := experimentPathInside(root, metrics); err != nil {
			return base, fmt.Errorf("refusing lane metrics output: %w", err)
		}
		physicalMetrics, err := experimentCanonicalPath(metrics)
		if err != nil {
			return base, err
		}
		physicalDB, err := experimentCanonicalPath(db)
		if err != nil {
			return base, err
		}
		for _, reserved := range []string{physicalDB, physicalDB + "-wal", physicalDB + "-shm", physicalDB + "-journal"} {
			if physicalMetrics == reserved {
				return base, fmt.Errorf("experiment metrics must not name the database or a SQLite sidecar")
			}
		}
		if _, err := os.Lstat(metrics); !os.IsNotExist(err) {
			return base, fmt.Errorf("experiment metrics require a fresh absent output file; existing files, aliases and symlinks are refused")
		}
	}
	if raw == "" {
		return base, nil
	}
	var in experimentBudgetInput
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return base, fmt.Errorf("decode experiment-only lane budget: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return base, fmt.Errorf("experiment-only lane budget must contain one JSON object")
	}
	b := base
	if in.BatchRecords != nil {
		b.BatchRecords = *in.BatchRecords
	}
	if in.BatchBytes != nil {
		b.BatchBytes = *in.BatchBytes
	}
	if in.QueueRecords != nil {
		b.QueueRecords = *in.QueueRecords
	}
	if in.QueueBytes != nil {
		b.QueueBytes = *in.QueueBytes
	}
	if in.FlushMilliseconds != nil {
		b.FlushInterval = time.Duration(*in.FlushMilliseconds) * time.Millisecond
	}
	if err := b.Validate(); err != nil {
		return base, fmt.Errorf("refusing experiment-only lane budget: %w", err)
	}
	return b, nil
}
