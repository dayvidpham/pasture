package writelane

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"github.com/dayvidpham/provenance"
)

type boundaryRecord struct{ prepare func() }

func (r boundaryRecord) Prepare(context.Context) (provenance.OperationInput, error) {
	r.prepare()
	return provenance.OperationInput{}, nil
}
func (boundaryRecord) Settle(context.Context, provenance.BatchRecordResult) error { return nil }

func TestProfileQueueEndsForWholeBatchBeforeAnyPrepare(t *testing.T) {
	t.Parallel()
	var observed []string
	lane := &Lane{ctx: context.Background(), apply: func(_ context.Context, in []provenance.OperationInput) (provenance.BatchResult, error) {
		return provenance.BatchResult{Records: make([]provenance.BatchRecordResult, len(in))}, nil
	}}
	var batch []*item
	for _, name := range []string{"a", "b"} {
		batch = append(batch, &item{queueDone: func() { observed = append(observed, "queue_"+name) },
			admissionToPrepareDone: func() { observed = append(observed, "own_"+name) },
			record:                 boundaryRecord{prepare: func() { observed = append(observed, "prepare_"+name) }}, result: make(chan error, 1)})
	}
	lane.commitBatch(batch)
	want := []string{"queue_a", "queue_b", "own_a", "prepare_a", "own_b", "prepare_b"}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("profiling boundaries = %v, want %v", observed, want)
	}
}

// This checks placement rather than wall-clock thresholds. Moving a queue end
// into the per-record loop must fail even when Prepare is instant in a test.
func TestProfileTimerBoundaryStructure(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "lane.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				calls[fn.Name.Name] = append(calls[fn.Name.Name], sel.Sel.Name)
			}
			return true
		})
	}
	index := func(fn, call string) int {
		for i, name := range calls[fn] {
			if name == call {
				return i
			}
		}
		return -1
	}
	if index("commitBatch", "queueDone") < 0 || index("commitBatch", "queueDone") >= index("commitBatch", "commitRecords") || index("commitRecords", "queueDone") != -1 {
		t.Fatal("queue timer must end for the batch before commitRecords, never before individual Prepare")
	}
	for _, timer := range []string{"batchPrepareWaitDone", "admissionToPrepareDone"} {
		if index("commitRecords", timer) < 0 || index("commitRecords", timer) >= index("commitRecords", "Prepare") {
			t.Fatalf("%s must end before own Prepare", timer)
		}
	}
}
