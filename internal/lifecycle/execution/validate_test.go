package execution_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
)

func TestValidateMetadataRejectsDamagedEvidence(t *testing.T) {
	t.Parallel()
	metadata, err := execution.Normalize(execution.Inputs{Kind: execution.Primary, Host: execution.Declaration{
		Model:    execution.Raw{Present: true, Member: execution.HostModelID, Value: "model"},
		Provider: execution.Raw{Present: true, Member: execution.HostProvider, Value: "provider"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.ValidateMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	metadata.Provider.Candidates = []execution.Candidate{{Source: execution.Host, Field: execution.HostProvider, Value: "secret\n"}}
	if err := execution.ValidateMetadata(metadata); err == nil {
		t.Fatal("accepted malformed metadata")
	}
}
