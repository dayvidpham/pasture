package testutil_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// recordingTB captures the failures the helper reports so the helper's own
// messages can be pinned without failing this test.
type recordingTB struct {
	testing.TB
	errors []string
	fatal  []string
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatal = append(r.fatal, fmt.Sprintf(format, args...))
}

// sample is a small enum with a sentinel, the shape the helper expects its
// callers to derive their arm list from.
type sample uint8

const (
	sampleInvalid sample = iota
	sampleOne
	sampleTwo
	sampleThree
	numSamples
)

func sampleArms() []sample {
	out := []sample{}
	for s := sampleOne; s < numSamples; s++ {
		out = append(out, s)
	}
	return out
}

func TestRequireEnumMirrorCompleteNamesEveryUnmappedArm(t *testing.T) {
	t.Parallel()
	complete := map[sample]string{sampleOne: "one", sampleTwo: "two", sampleThree: "three"}
	mirror := func(table map[sample]string) func(sample) (string, bool) {
		return func(arm sample) (string, bool) {
			text, ok := table[arm]
			return text, ok
		}
	}

	recorder := &recordingTB{}
	testutil.RequireEnumMirrorComplete(recorder, testutil.EnumMirror[sample]{Subject: "sample -> names", Arms: sampleArms(), Mirror: mirror(complete)})
	require.Empty(t, recorder.errors, "a complete mirror reports nothing")
	require.Empty(t, recorder.fatal)

	// A new arm added above the old last arm is in the derived population and
	// has no mapping: the helper names it.
	missingThree := map[sample]string{sampleOne: "one", sampleTwo: "two"}
	recorder = &recordingTB{}
	testutil.RequireEnumMirrorComplete(recorder, testutil.EnumMirror[sample]{Subject: "sample -> names", Arms: sampleArms(), Mirror: mirror(missingThree)})
	require.Len(t, recorder.errors, 1)
	require.Contains(t, recorder.errors[0], "enum mirror sample -> names: 1 source arm(s) have no mapping on the mirror side: [3]")
	require.Contains(t, recorder.errors[0], "add the mapping for each named arm")

	recorder = &recordingTB{}
	testutil.RequireEnumMirrorComplete(recorder, testutil.EnumMirror[sample]{
		Subject:  "sample -> names",
		Arms:     sampleArms(),
		Mirror:   mirror(missingThree),
		Describe: func(arm sample) string { return fmt.Sprintf("sample(%d)", arm) },
	})
	require.Len(t, recorder.errors, 1)
	require.Contains(t, recorder.errors[0], "[sample(3)]", "the caller's description of the arm is what the message carries")

	collided := map[sample]string{sampleOne: "one", sampleTwo: "one", sampleThree: "three"}
	recorder = &recordingTB{}
	testutil.RequireEnumMirrorComplete(recorder, testutil.EnumMirror[sample]{Subject: "sample -> names", Arms: sampleArms(), Mirror: mirror(collided)})
	require.Len(t, recorder.errors, 1)
	require.Contains(t, recorder.errors[0], `two source arms share one mirror representation: [1 and 2 both map to "one"]`)

	recorder = &recordingTB{}
	testutil.RequireEnumMirrorComplete(recorder, testutil.EnumMirror[sample]{Subject: "sample -> names", Arms: nil, Mirror: mirror(complete)})
	require.Len(t, recorder.fatal, 1)
	require.True(t, strings.Contains(recorder.fatal[0], "the source arm list is empty, so no mapping was checked"), recorder.fatal[0])
}
