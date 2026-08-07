package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/lifecycle/codebook"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

// HookLifecycleCodebookInput selects the codebook read surface's store and
// whether the canonical body is included in the output.
type HookLifecycleCodebookInput struct {
	DBPath string
	Body   bool
}

// HookLifecycleCodebook prints the active codebook coordinate (id, version,
// content digest), whether that coordinate is journaled (the end-to-end
// definition-resolution proof), and optionally the canonical body. It is
// strictly read-only: it never activates the codebook.
func HookLifecycleCodebook(ctx context.Context, out io.Writer, in HookLifecycleCodebookInput, format string) (int, error) {
	if ctx == nil || out == nil || (format != "text" && format != "json") {
		return listResult(fmt.Errorf("show lifecycle codebook: context, output, and format text|json are required"))
	}
	book := codebook.Active()
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return listResult(err)
	}
	defer tracker.Close()
	ref, journaled, err := receipt.ResolveActiveCodebook(tracker.Journal())
	if err != nil {
		return listResult(err)
	}
	definitionJournalID := provenance.JournalID(ref.Definition.Definition.JournalID())

	if format == "json" {
		view := codebookView{
			ID:        string(book.ID),
			Version:   book.Version,
			Content:   hex.EncodeToString(book.Content[:]),
			Journaled: journaled,
		}
		if journaled {
			id := int64(definitionJournalID)
			view.DefinitionJournalID = &id
		}
		if in.Body {
			view.Body = string(codebook.Body())
		}
		if err := json.NewEncoder(out).Encode(view); err != nil {
			return listResult(err)
		}
		return 0, nil
	}

	if _, err := fmt.Fprintf(out, "codebook: %s\nversion: %d\ncontent: %s\n", book.ID, book.Version, hex.EncodeToString(book.Content[:])); err != nil {
		return listResult(err)
	}
	if journaled {
		if _, err := fmt.Fprintf(out, "journaled: true (definition journal id %d)\n", definitionJournalID); err != nil {
			return listResult(err)
		}
	} else if _, err := fmt.Fprintf(out, "journaled: false (no delivery has activated this codebook yet)\n"); err != nil {
		return listResult(err)
	}
	if in.Body {
		if _, err := fmt.Fprintf(out, "body: %s\n", codebook.Body()); err != nil {
			return listResult(err)
		}
	}
	return 0, nil
}

type codebookView struct {
	ID                  string `json:"id"`
	Version             uint32 `json:"version"`
	Content             string `json:"content"`
	Journaled           bool   `json:"journaled"`
	DefinitionJournalID *int64 `json:"definitionJournalId,omitempty"`
	Body                string `json:"body,omitempty"`
}
