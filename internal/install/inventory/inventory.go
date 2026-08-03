// Package inventory is the confirmed installation inventory: one typed record
// per cell that Pasture actually mutated, plus absent tombstones for removed
// cells.
//
// It is factual operational evidence, separate from user-preference YAML. It
// answers what Pasture installed, whether an uninstall completed, what remains
// or is unknown, the control source and native-trust disposition, and the exact
// retry. Records serialize in the frozen nine-cell order and reject unknown or
// duplicate cells. The file is written with a mode-0600 symlink-safe atomic
// replace; a symlink, non-regular, or group/other-accessible state file is
// rejected before any read, so the confidentiality invariant Pasture establishes
// on write is re-checked (not silently repaired) on every read.
package inventory

import (
	"fmt"
	"os"
	"sort"

	"github.com/dayvidpham/pasture/internal/install/cell"
)

// SchemaID is the frozen schema identifier for the confirmed-state document.
const SchemaID = "pasture.install.state/v1"

// Inventory is a validated set of confirmed-state records, at most one per cell.
type Inventory struct {
	records map[string]Record // keyed by cell.String()
}

// New returns an empty inventory.
func New() Inventory {
	return Inventory{records: map[string]Record{}}
}

// Upsert stores (replacing any prior record for the same cell) a confirmed
// record. It rejects an invalid record so no partial state is stored.
func (inv *Inventory) Upsert(record Record) error {
	if !record.IsValid() {
		return cell.NewFault(
			"inventory upsert", "valid record",
			"the record is the invalid zero value",
			"internal/install/inventory.Upsert", "recording a confirmed cell state",
			"invalid state would be persisted",
			"construct the record with NewRecord", nil,
		)
	}
	inv.records[record.Cell().String()] = record
	return nil
}

// Lookup returns the record for a cell, if any.
func (inv Inventory) Lookup(c cell.Cell) (Record, bool) {
	r, ok := inv.records[c.String()]
	return r, ok
}

// Ordered returns records sorted in the frozen canonical cell order.
func (inv Inventory) Ordered() []Record {
	out := make([]Record, 0, len(inv.records))
	for _, r := range inv.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Cell().Index() < out[j].Cell().Index()
	})
	return out
}

// Len returns the number of stored records.
func (inv Inventory) Len() int { return len(inv.records) }

// Load reads and decodes the confirmed-state file. A missing file yields an
// empty inventory; a symlink, non-regular, or group/other-accessible state file
// is rejected before read.
func Load(path string) (Inventory, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return Inventory{}, cell.NewFault(
			"inventory load", "inspectable state file",
			fmt.Sprintf("the state file could not be inspected: %v", err),
			path, "checking the state file type before read",
			"prior confirmed state cannot be reconciled",
			"ensure the state file path is accessible, then retry", err,
		)
	}
	if !info.Mode().IsRegular() {
		return Inventory{}, cell.NewFault(
			"inventory load", "regular state file",
			fmt.Sprintf("the state file is a %s, not a regular file", info.Mode().Type()),
			path, "checking the state file type before read",
			"a symlink or special file could redirect trust to attacker-controlled bytes",
			"remove the non-regular entry so Pasture manages a regular state file", nil,
		)
	}
	// Enforce (not repair) the mode-0600 confidentiality invariant on read. A
	// file with any group or other permission is rejected rather than chmod-ed in
	// place, because repairing would mean writing to a file Pasture has not yet
	// validated as its own; the user resolves it explicitly.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return Inventory{}, cell.NewFault(
			"inventory load", "owner-only (0600) state file",
			fmt.Sprintf("the state file mode is %04o, which grants group or other access", perm),
			path, "checking the state file permissions before read",
			"a world- or group-readable confirmed-state file leaks what Pasture installed, and a group/other-writable one could be tampered with between Pasture runs",
			fmt.Sprintf("restore owner-only access with: chmod 0600 %s", path), nil,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Inventory{}, cell.NewFault(
			"inventory load", "readable state file",
			fmt.Sprintf("the state file could not be read: %v", err),
			path, "reading the confirmed-state file",
			"prior confirmed state cannot be reconciled",
			"ensure the state file is readable, then retry", err,
		)
	}
	return Parse(data)
}

// Save atomically writes the inventory at mode 0600.
func Save(path string, inv Inventory) error {
	data, err := inv.Marshal()
	if err != nil {
		return err
	}
	return writeState(path, data)
}
