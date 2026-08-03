package formatters

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

func HookLifecycle(w io.Writer, page model.LifecyclePage, format string) error {
	if format == "json" {
		items := make([]lifecycleJSONRecord, 0, len(page.Records()))
		for _, record := range page.Records() {
			interpreted := record.Interpreted()
			views := make([]lifecycleJSONInterpreted, 0, len(interpreted))
			for _, value := range interpreted {
				views = append(views, lifecycleJSONInterpreted{JournalID: value.JournalID(), Semantic: value.Semantic(), Identities: value.Identities(), Unresolved: value.UnresolvedFacts(), Contract: value.Contract().String()})
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
		if values := item.Interpreted(); len(values) == 1 {
			interpretedContract = values[0].Contract().String()
		}
		if _, err := fmt.Fprintf(w, "%d\t%d\tregistration=%s\tinterpreted=%s\n", item.Occurrence.JournalID(), item.Occurrence.Kind, item.Occurrence.RuntimeContract, interpretedContract); err != nil {
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
	JournalID  any    `json:"journalId"`
	Semantic   any    `json:"semantic"`
	Identities any    `json:"identities"`
	Unresolved any    `json:"unresolved"`
	Contract   string `json:"contract"`
}
