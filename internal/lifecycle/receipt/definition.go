package receipt

import (
	"context"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/codebook"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// EnsureActiveCodebook lazily and idempotently journals the active codebook
// definition, returning the reference to the journaled definition. It is called
// on the valid-capture delivery path BEFORE the delivery receipt is written, so
// a committed interpreted.v2 record can never cite an unjournaled codebook.
//
// Steady state is one bounded, content-addressed existence check and zero
// writes: the definition-activation operation identity is derived from the
// codebook content, so LookupCommitted resolves an already-active codebook
// without scanning. On the FIRST delivery the definition is absent, so the
// activation is legalized through the gate and committed. Concurrency is safe by
// construction: the deterministic operation identity makes two racing first
// deliveries collapse to exactly one committed activation (the loser's Commit
// returns the same result short-circuited), so neither double-activates nor
// references an unjournaled codebook (F11 replay arbiter, verified benign).
func EnsureActiveCodebook(ctx context.Context, s Service) (model.CodebookDefinitionRef, error) {
	if s.Appender.Journal == nil {
		return model.CodebookDefinitionRef{}, structured(pasterrors.CategoryValidation,
			"The lifecycle receipt service cannot ensure the active codebook.",
			"Journaling the codebook definition requires the provenance journal.",
			"Ensuring the active codebook definition (internal/lifecycle/receipt/definition.go in receipt.EnsureActiveCodebook).",
			"No codebook definition was resolved or committed.",
			"Construct the service through tasks.NewLifecycleReceiptService.", nil)
	}
	book := codebook.Active()
	write, err := NewDefinitionActivation(book, codebook.Body())
	if err != nil {
		return model.CodebookDefinitionRef{}, err
	}

	// Bounded, content-addressed existence check: the operation identity is
	// derived from the codebook content, so an already-active codebook resolves
	// with a single indexed lookup and no writes.
	committed, err := s.Appender.Journal.LookupCommitted(write.OperationID())
	if err != nil {
		return model.CodebookDefinitionRef{}, structured(pasterrors.CategoryStorage,
			"The active codebook definition could not be looked up.",
			"The provenance journal rejected the bounded operation-identity lookup.",
			"Ensuring the active codebook definition (internal/lifecycle/receipt/definition.go in receipt.EnsureActiveCodebook).",
			"It is unknown whether the codebook is already journaled; no delivery should proceed until this succeeds.",
			"Confirm journal health and retry.", err)
	}
	if committed.Kind == provenance.CommittedExact {
		return definitionRefFromSlots(book, committed.ResultSlots)
	}

	// Absent: legalize the activation through the write gate and commit. The
	// commit is race-safe (deterministic operation identity), so a concurrent
	// first delivery is admitted as benign-already-activated.
	intent, refusal := gate.NewDefinitionActivationIntent(book.Content)
	if refusal != nil {
		return model.CodebookDefinitionRef{}, refusal
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		return model.CodebookDefinitionRef{}, refusal
	}
	receipt, err := s.Commit(ctx, warrant, write)
	if err != nil {
		return model.CodebookDefinitionRef{}, err
	}
	return model.CodebookDefinitionRef{
		Definition: model.DefinitionRef{
			Definition: model.DefinitionJournalID(receipt.Definition),
			Kind:       model.DefinitionCodebook,
			Content:    book.Content,
		},
	}, nil
}

func definitionRefFromSlots(book model.CodebookCoordinate, slots []provenance.ResultSlotBinding) (model.CodebookDefinitionRef, error) {
	for _, slot := range slots {
		if slot.Slot == definitionSlot && slot.ProducedJournalID > 0 {
			return model.CodebookDefinitionRef{
				Definition: model.DefinitionRef{
					Definition: model.DefinitionJournalID(slot.ProducedJournalID),
					Kind:       model.DefinitionCodebook,
					Content:    book.Content,
				},
			}, nil
		}
	}
	return model.CodebookDefinitionRef{}, structured(pasterrors.CategoryStorage,
		"The journaled codebook definition is missing its definition result slot.",
		"An already-committed definition-activation operation did not expose the definition slot.",
		"Ensuring the active codebook definition (internal/lifecycle/receipt/definition.go in receipt.EnsureActiveCodebook).",
		"The codebook coordinate cannot be resolved to a journaled definition.",
		"Inspect the committed definition-activation operation and repair the journal.",
		fmt.Errorf("missing result slot %q", definitionSlot))
}
