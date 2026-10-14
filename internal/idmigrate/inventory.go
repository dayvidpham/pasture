package idmigrate

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// blobRow is one raw lifecycle payload blob exactly as stored:
// (digest, body bytes, byte_count, written_at). The inventory keeps every one of
// these, not a row count, because a conversion that swapped two equal-length
// bodies would leave the count unchanged and lose raw evidence.
type blobRow struct {
	body      []byte
	byteCount int
	writtenAt int64
}

// captureBlobInventory records every lifecycle_payload_blobs row on the caller's
// transaction. Raw host evidence is never rewritten by conversion; this is what
// proves it.
func captureBlobInventory(ctx context.Context, tx *sql.Tx) (map[string]blobRow, error) {
	inventory := map[string]blobRow{}
	exists, err := tableExists(ctx, tx, "lifecycle_payload_blobs")
	if err != nil {
		return nil, err
	}
	if !exists {
		return inventory, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT digest, body, byte_count, written_at FROM lifecycle_payload_blobs ORDER BY digest`)
	if err != nil {
		return nil, conversionError("Pasture couldn't capture the raw payload-blob inventory.", "The read-only inventory SELECT failed inside the conversion transaction.", "No store data was changed; the conversion aborts before any write.", "Confirm the store is readable and retry.", err)
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		var item blobRow
		if err := rows.Scan(&digest, &item.body, &item.byteCount, &item.writtenAt); err != nil {
			return nil, conversionError("Pasture couldn't scan a raw payload blob.", err.Error(), "No store data was changed.", "Restore the store from the archive and retry.", err)
		}
		inventory[digest] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return inventory, nil
}

// compareBlobInventories requires the post-conversion raw inventory to equal the
// pre-conversion one byte for byte. A missing row, a changed body, a changed
// length or a changed stamp fails the conversion: the raw evidence is not the
// conversion's to reclaim, and the conversion reports zero reclaimed bodies.
func compareBlobInventories(before, after map[string]blobRow) error {
	if len(before) != len(after) {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "The raw payload-blob inventory changed during the identity conversion.",
			Why:      fmt.Sprintf("The store had %d raw payload blob(s) before conversion and %d after.", len(before), len(after)),
			Where:    "Comparing the raw evidence inventory (internal/idmigrate/inventory.go in idmigrate.compareBlobInventories).",
			Impact:   "The whole conversion transaction rolls back; no marker is published and the raw evidence is unchanged on disk.",
			Fix:      "Report this as a conversion defect — conversion must never add or remove raw payload blobs — and restore the archive.",
		}
	}
	for digest, want := range before {
		got, ok := after[digest]
		if !ok {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The raw payload blob %s disappeared during the identity conversion.", digest),
				Why:      "A raw evidence row present before conversion is absent after it.",
				Where:    "Comparing the raw evidence inventory (internal/idmigrate/inventory.go in idmigrate.compareBlobInventories).",
				Impact:   "The whole conversion transaction rolls back; the raw evidence is unchanged on disk.",
				Fix:      "Report this as a conversion defect and restore the archive.",
			}
		}
		if got.byteCount != want.byteCount || got.writtenAt != want.writtenAt || !bytes.Equal(got.body, want.body) {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The raw payload blob %s changed during the identity conversion.", digest),
				Why:      "The stored body bytes, byte count or written-at stamp differ from the pre-conversion inventory.",
				Where:    "Comparing the raw evidence inventory (internal/idmigrate/inventory.go in idmigrate.compareBlobInventories).",
				Impact:   "The whole conversion transaction rolls back; the raw evidence is unchanged on disk.",
				Fix:      "Report this as a conversion defect — raw host evidence is opaque and must stay byte-exact — and restore the archive.",
			}
		}
	}
	return nil
}
