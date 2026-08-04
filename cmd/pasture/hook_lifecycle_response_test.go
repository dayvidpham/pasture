package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type canonicalLifecycleResponse struct{}

func (canonicalLifecycleResponse) MarshalJSON() ([]byte, error) {
	return []byte(`{"decision":"proceed"}`), nil
}

func TestWriteLifecycleResponseWritesOnlyCanonicalJSON(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	require.NoError(t, writeLifecycleResponse(&output, canonicalLifecycleResponse{}))
	require.Equal(t, `{"decision":"proceed"}`, output.String())
}

type failingLifecycleWriter struct{}

func (failingLifecycleWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }

func TestWriteLifecycleResponseReportsOutputFailure(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, writeLifecycleResponse(failingLifecycleWriter{}, canonicalLifecycleResponse{}), "closed output")
}
