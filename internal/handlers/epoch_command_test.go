package handlers

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/tasks"
)

func TestParseEpochCommandReviewSubmission_StrictSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "plan review",
			input: `{"verdict":"accept","feedback":[]}`,
		},
		{
			name:  "implementation review",
			input: `{"verdict":"accept","findings":[]}`,
		},
		{
			name:    "unknown top level field",
			input:   `{"verdict":"accept","feedback":[],"extra":true}`,
			wantErr: true,
		},
		{
			name:    "unknown nested field",
			input:   `{"verdict":"revise","feedback":[{"body":"Clarify the acceptance condition.","extra":true}]}`,
			wantErr: true,
		},
		{
			name:    "duplicate field",
			input:   `{"verdict":"accept","verdict":"revise","feedback":[]}`,
			wantErr: true,
		},
		{
			name:    "trailing JSON",
			input:   `{"verdict":"accept","feedback":[]}{}`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseEpochCommandReviewSubmission("epoch review submit", []byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parse error = %v, want error=%t", err, tc.wantErr)
			}
		})
	}
}

func TestExecuteEpochCommandWritesOneSemanticJSONResult(t *testing.T) {
	t.Parallel()

	store := &epochCommandTestStore{}
	var output bytes.Buffer
	err := executeEpochCommand(context.Background(), EpochCommandInvocation{Output: &output}, preparedEpochCommand{
		command: "epoch test",
		invoke: func(context.Context, epochCommandStore, tasks.EpochService) (any, error) {
			return struct {
				Epoch  string `json:"epoch"`
				Status string `json:"status"`
			}{Epoch: "epoch-example", Status: "recorded"}, nil
		},
	}, func(string) (epochCommandStore, error) {
		return store, nil
	})
	if err != nil {
		t.Fatalf("executeEpochCommand: %v", err)
	}
	if !store.closed {
		t.Fatal("store was not closed after successful command")
	}
	if got, want := output.String(), "{\"epoch\":\"epoch-example\",\"status\":\"recorded\"}\n"; got != want {
		t.Fatalf("semantic output = %q, want %q", got, want)
	}
}

func TestExecuteEpochCommandClosesStoreAfterServiceConstructionFailure(t *testing.T) {
	t.Parallel()

	store := &epochCommandTestStore{serviceErr: errors.New("service unavailable")}
	var output bytes.Buffer
	err := executeEpochCommand(context.Background(), EpochCommandInvocation{Output: &output}, preparedEpochCommand{command: "epoch test"}, func(string) (epochCommandStore, error) {
		return store, nil
	})
	if err == nil {
		t.Fatal("executeEpochCommand succeeded with a failed service constructor")
	}
	if !store.closed {
		t.Fatal("store was not closed after service construction failure")
	}
}

type epochCommandTestStore struct {
	closed     bool
	serviceErr error
}

func (s *epochCommandTestStore) NewEpochService(tasks.EpochServiceOptions) (tasks.EpochService, error) {
	if s.serviceErr != nil {
		return nil, s.serviceErr
	}
	return epochCommandTestService{}, nil
}

func (s *epochCommandTestStore) Show(provenance.TaskID) (provenance.Task, error) {
	return provenance.Task{}, errors.New("unexpected task lookup")
}

func (s *epochCommandTestStore) Close() error {
	s.closed = true
	return nil
}

type epochCommandTestService struct {
	tasks.EpochService
}
