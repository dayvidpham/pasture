package temporal_test

import (
	"context"
	"errors"
	"testing"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"google.golang.org/grpc"

	"github.com/dayvidpham/pasture/internal/temporal"
)

// ─── Mock types ───────────────────────────────────────────────────────────────

// mockOperatorServiceClient implements operatorservice.OperatorServiceClient for tests.
// Only ListSearchAttributes and AddSearchAttributes are exercised by EnsureSearchAttributes;
// all other methods return an "not implemented" error.
type mockOperatorServiceClient struct {
	listAttrs  map[string]enumspb.IndexedValueType
	listErr    error
	addErr     error
	addCalled  bool
	addedAttrs map[string]enumspb.IndexedValueType
}

func (m *mockOperatorServiceClient) ListSearchAttributes(
	_ context.Context,
	_ *operatorservice.ListSearchAttributesRequest,
	_ ...grpc.CallOption,
) (*operatorservice.ListSearchAttributesResponse, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &operatorservice.ListSearchAttributesResponse{
		CustomAttributes: m.listAttrs,
	}, nil
}

func (m *mockOperatorServiceClient) AddSearchAttributes(
	_ context.Context,
	req *operatorservice.AddSearchAttributesRequest,
	_ ...grpc.CallOption,
) (*operatorservice.AddSearchAttributesResponse, error) {
	m.addCalled = true
	if m.addErr != nil {
		return nil, m.addErr
	}
	if m.addedAttrs == nil {
		m.addedAttrs = make(map[string]enumspb.IndexedValueType)
	}
	for k, v := range req.SearchAttributes {
		m.addedAttrs[k] = v
	}
	return &operatorservice.AddSearchAttributesResponse{}, nil
}

// Remaining OperatorServiceClient interface methods — not called by EnsureSearchAttributes.

func (m *mockOperatorServiceClient) RemoveSearchAttributes(_ context.Context, _ *operatorservice.RemoveSearchAttributesRequest, _ ...grpc.CallOption) (*operatorservice.RemoveSearchAttributesResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) DeleteNamespace(_ context.Context, _ *operatorservice.DeleteNamespaceRequest, _ ...grpc.CallOption) (*operatorservice.DeleteNamespaceResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) AddOrUpdateRemoteCluster(_ context.Context, _ *operatorservice.AddOrUpdateRemoteClusterRequest, _ ...grpc.CallOption) (*operatorservice.AddOrUpdateRemoteClusterResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) RemoveRemoteCluster(_ context.Context, _ *operatorservice.RemoveRemoteClusterRequest, _ ...grpc.CallOption) (*operatorservice.RemoveRemoteClusterResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) ListClusters(_ context.Context, _ *operatorservice.ListClustersRequest, _ ...grpc.CallOption) (*operatorservice.ListClustersResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) GetNexusEndpoint(_ context.Context, _ *operatorservice.GetNexusEndpointRequest, _ ...grpc.CallOption) (*operatorservice.GetNexusEndpointResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) CreateNexusEndpoint(_ context.Context, _ *operatorservice.CreateNexusEndpointRequest, _ ...grpc.CallOption) (*operatorservice.CreateNexusEndpointResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) UpdateNexusEndpoint(_ context.Context, _ *operatorservice.UpdateNexusEndpointRequest, _ ...grpc.CallOption) (*operatorservice.UpdateNexusEndpointResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) DeleteNexusEndpoint(_ context.Context, _ *operatorservice.DeleteNexusEndpointRequest, _ ...grpc.CallOption) (*operatorservice.DeleteNexusEndpointResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockOperatorServiceClient) ListNexusEndpoints(_ context.Context, _ *operatorservice.ListNexusEndpointsRequest, _ ...grpc.CallOption) (*operatorservice.ListNexusEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

// mockOperatorProvider implements temporal.OperatorServiceProvider for tests.
type mockOperatorProvider struct {
	op *mockOperatorServiceClient
}

func (p *mockOperatorProvider) OperatorService() operatorservice.OperatorServiceClient {
	return p.op
}

// ─── EnsureSearchAttributes Tests ─────────────────────────────────────────────

// TestEnsureSearchAttributes_AllMissing verifies that all required attributes
// are registered when none are present in the namespace.
func TestEnsureSearchAttributes_AllMissing(t *testing.T) {
	t.Parallel()
	op := &mockOperatorServiceClient{
		listAttrs: map[string]enumspb.IndexedValueType{}, // nothing registered yet
	}
	provider := &mockOperatorProvider{op: op}

	err := temporal.EnsureSearchAttributes(context.Background(), provider, "test-ns", nil)
	if err != nil {
		t.Fatalf("EnsureSearchAttributes: unexpected error: %v", err)
	}
	if !op.addCalled {
		t.Error("expected AddSearchAttributes to be called for missing attributes, but it was not")
	}
	// All 6 required attributes should have been added.
	if len(op.addedAttrs) != 6 {
		t.Errorf("expected 6 attributes registered, got %d: %v", len(op.addedAttrs), op.addedAttrs)
	}
}

// TestEnsureSearchAttributes_NoneToAdd verifies that AddSearchAttributes is NOT
// called when all required attributes already exist.
func TestEnsureSearchAttributes_NoneToAdd(t *testing.T) {
	t.Parallel()
	// Populate all required attribute names.
	existingAttrs := map[string]enumspb.IndexedValueType{
		temporal.SAEpochId:       enumspb.INDEXED_VALUE_TYPE_TEXT,
		temporal.SAPhase:         enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		temporal.SARole:          enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		temporal.SAStatus:        enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		temporal.SADomain:        enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		temporal.SALastEventType: enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	}
	op := &mockOperatorServiceClient{listAttrs: existingAttrs}
	provider := &mockOperatorProvider{op: op}

	err := temporal.EnsureSearchAttributes(context.Background(), provider, "default", nil)
	if err != nil {
		t.Fatalf("EnsureSearchAttributes: unexpected error: %v", err)
	}
	if op.addCalled {
		t.Error("AddSearchAttributes should NOT be called when all attributes already exist")
	}
}

// TestEnsureSearchAttributes_ListError verifies that a ListSearchAttributes
// failure surfaces as a wrapped error.
func TestEnsureSearchAttributes_ListError(t *testing.T) {
	t.Parallel()
	listErr := errors.New("grpc: connection refused")
	op := &mockOperatorServiceClient{
		listErr: listErr,
	}
	provider := &mockOperatorProvider{op: op}

	err := temporal.EnsureSearchAttributes(context.Background(), provider, "default", nil)
	if err == nil {
		t.Fatal("expected error from ListSearchAttributes failure, got nil")
	}
	if !errors.Is(err, listErr) {
		t.Errorf("expected wrapped listErr, got: %v", err)
	}
}

// TestEnsureSearchAttributes_AddError verifies that an AddSearchAttributes
// failure surfaces as a wrapped error.
func TestEnsureSearchAttributes_AddError(t *testing.T) {
	t.Parallel()
	addErr := errors.New("permission denied")
	op := &mockOperatorServiceClient{
		listAttrs: map[string]enumspb.IndexedValueType{}, // all missing
		addErr:    addErr,
	}
	provider := &mockOperatorProvider{op: op}

	err := temporal.EnsureSearchAttributes(context.Background(), provider, "default", nil)
	if err == nil {
		t.Fatal("expected error from AddSearchAttributes failure, got nil")
	}
	if !errors.Is(err, addErr) {
		t.Errorf("expected wrapped addErr, got: %v", err)
	}
}

// TestEnsureSearchAttributes_PartialMissing verifies that only the missing
// attributes are registered when some already exist.
func TestEnsureSearchAttributes_PartialMissing(t *testing.T) {
	t.Parallel()
	// Provide some (but not all) required attributes.
	existingAttrs := map[string]enumspb.IndexedValueType{
		temporal.SAEpochId: enumspb.INDEXED_VALUE_TYPE_TEXT,
		temporal.SAPhase:   enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	}
	op := &mockOperatorServiceClient{listAttrs: existingAttrs}
	provider := &mockOperatorProvider{op: op}

	err := temporal.EnsureSearchAttributes(context.Background(), provider, "default", nil)
	if err != nil {
		t.Fatalf("EnsureSearchAttributes: unexpected error: %v", err)
	}
	if !op.addCalled {
		t.Error("expected AddSearchAttributes to be called for the 4 missing attributes")
	}
	// 4 attributes should have been added (6 required - 2 existing).
	if len(op.addedAttrs) != 4 {
		t.Errorf("expected 4 attributes added, got %d: %v", len(op.addedAttrs), op.addedAttrs)
	}
	// Existing attributes should NOT be re-added.
	if _, ok := op.addedAttrs[temporal.SAEpochId]; ok {
		t.Error("SAEpochId should not be re-added; it already existed")
	}
	if _, ok := op.addedAttrs[temporal.SAPhase]; ok {
		t.Error("SAPhase should not be re-added; it already existed")
	}
}
