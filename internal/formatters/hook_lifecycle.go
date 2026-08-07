package formatters

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// codebookUnresolved is the disclosure the read surface renders for a committed
// interpreted.v1 record — it predates the codebook producer (M5), so its
// interpretation coordinate is not resolvable rather than invented.
const codebookUnresolved = "unresolved (pre-M5)"

func HookLifecycle(w io.Writer, page model.LifecyclePage, format string) error {
	if format == "json" {
		items := make([]lifecycleJSONRecord, 0, len(page.Records()))
		for _, record := range page.Records() {
			interpreted := record.Interpreted()
			views := make([]lifecycleJSONInterpreted, 0, len(interpreted))
			for _, value := range interpreted {
				view := lifecycleJSONInterpreted{JournalID: value.JournalID(), Semantic: value.Semantic(), Identities: value.Identities(), Unresolved: value.UnresolvedFacts(), Contract: value.Contract().String()}
				if book, ok := value.Codebook(); ok {
					view.Codebook = &lifecycleJSONCodebook{ID: string(book.ID), Version: book.Version, Content: hex.EncodeToString(book.Content[:])}
				}
				views = append(views, view)
			}
			items = append(items, lifecycleJSONRecord{JournalID: record.Occurrence.JournalID(), Event: record.Occurrence.Kind, RegistrationContract: record.Occurrence.RuntimeContract.String(), Capture: record.Occurrence.Capture, PayloadDigest: record.Occurrence.Payload.Digest.String(), Interpreted: views})
		}
		next := ""
		if page.State.Next != nil {
			var err error
			next, err = model.EncodeCursor(*page.State.Next)
			if err != nil {
				return err
			}
		}
		return json.NewEncoder(w).Encode(struct {
			Items []lifecycleJSONRecord `json:"items"`
			Next  string                `json:"nextCursor,omitempty"`
		}{Items: items, Next: next})
	}
	for _, item := range page.Records() {
		interpretedContract := "-"
		codebookColumn := "-"
		if values := item.Interpreted(); len(values) == 1 {
			interpretedContract = values[0].Contract().String()
			codebookColumn = codebookColumnText(values[0])
		}
		if _, err := fmt.Fprintf(w, "%d\t%d\tregistration=%s\tinterpreted=%s\tcodebook=%s\n", item.Occurrence.JournalID(), item.Occurrence.Kind, item.Occurrence.RuntimeContract, interpretedContract, codebookColumn); err != nil {
			return err
		}
	}
	if page.State.Next != nil {
		encoded, err := model.EncodeCursor(*page.State.Next)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "next-cursor=%s\n", encoded)
		return err
	}
	return nil
}

type lifecycleJSONRecord struct {
	JournalID            any                        `json:"journalId"`
	Event                model.ContractEventKind    `json:"event"`
	RegistrationContract string                     `json:"registrationContract"`
	Capture              model.CaptureDisposition   `json:"capture"`
	PayloadDigest        string                     `json:"payloadDigest"`
	Interpreted          []lifecycleJSONInterpreted `json:"interpreted"`
}
type lifecycleJSONInterpreted struct {
	JournalID  any                    `json:"journalId"`
	Semantic   any                    `json:"semantic"`
	Identities any                    `json:"identities"`
	Unresolved any                    `json:"unresolved"`
	Contract   string                 `json:"contract"`
	Codebook   *lifecycleJSONCodebook `json:"codebook,omitempty"`
}

type lifecycleJSONCodebook struct {
	ID      string `json:"id"`
	Version uint32 `json:"version"`
	Content string `json:"content"`
}

// codebookColumnText renders an interpreted record's codebook coordinate for the
// text read surface: the coordinate id, version, and a short content prefix for
// interpreted.v2, or the pre-M5 unresolved disclosure for interpreted.v1.
func codebookColumnText(record model.InterpretedRecord) string {
	book, ok := record.Codebook()
	if !ok {
		return codebookUnresolved
	}
	content := hex.EncodeToString(book.Content[:])
	if len(content) > 12 {
		content = content[:12]
	}
	return fmt.Sprintf("%s@%d#%s", book.ID, book.Version, content)
}
