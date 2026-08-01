package formatters

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

func HookLifecycle(w io.Writer, page model.LifecyclePage, format string) error {
	if format == "json" {
		items := page.Records()
		if items == nil {
			items = []model.LifecycleRecord{}
		}
		return json.NewEncoder(w).Encode(struct {
			Items []model.LifecycleRecord `json:"items"`
			Next  *model.Cursor           `json:"nextCursor,omitempty"`
		}{Items: items, Next: page.State.Next})
	}
	for _, item := range page.Records() {
		if _, err := fmt.Fprintf(w, "%d\t%d\t%s\tinterpretations=%d\n", item.Occurrence.JournalID(), item.Occurrence.Kind, item.Occurrence.RuntimeContract, len(item.Interpreted())); err != nil {
			return err
		}
	}
	if page.State.Next != nil {
		_, err := fmt.Fprintf(w, "next-cursor=%v\n", page.State.Next)
		return err
	}
	return nil
}
