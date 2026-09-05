package budget_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/engine/budget"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"modernc.org/sqlite"
)

const (
	loadWriters         = 48
	deliveriesPerWriter = 10
	maxP99Ratio         = 48
)

type operationSource struct {
	prefix string
	next   int
}

func (s *operationSource) NewOperationID() (string, error) {
	s.next++
	return fmt.Sprintf("pasture.budget.%s.%d", s.prefix, s.next), nil
}

type observation struct {
	Durations          []int64 `json:"durations_ns"`
	DeadlineFailures   int     `json:"deadline_failures"`
	ContentionFailures int     `json:"contention_failures"`
	StorageFailures    int     `json:"storage_failures"`
}
type observer struct{ result *observation }

func (o observer) ObserveOccurrenceCommit(duration time.Duration, err error) {
	o.result.Durations = append(o.result.Durations, int64(duration))
	var deadline model.IngressDeadlineError
	if stderrors.As(err, &deadline) {
		o.result.DeadlineFailures++
	}
}

type existingBlob struct{}

func (existingBlob) Put(context.Context, digest.Digest, []byte) error { return nil }

type lockAfterBlob struct {
	store    receipt.SQLiteBlobStore
	dbPath   string
	profile  timeouts.Profile
	held     chan struct{}
	release  chan struct{}
	lockErr  chan error
	probeErr chan error
}

func (s lockAfterBlob) Put(ctx context.Context, ref digest.Digest, body []byte) error {
	if err := s.store.Put(ctx, ref, body); err != nil {
		return err
	}
	go func() { s.lockErr <- budget.HoldWriter(ctx, s.dbPath, s.profile, s.held, s.release) }()
	select {
	case <-s.held:
		// The probe uses the constructor-wired blob store to occupy one fresh
		// profile-configured connection. The receipt appender then has to use
		// the second connection from the same pool and its real journal.
		go func() {
			s.probeErr <- s.store.Put(ctx, digest.FromString("pasture.budget.contention-probe"), []byte("contention probe"))
		}()
		if err := waitForStoreConnection(ctx, s.store); err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSliceStartAvailabilityUnderHookWriteLoad(t *testing.T) {
	if os.Getenv("PASTURE_RUN_LOAD_TESTS") != "1" {
		t.Skip("named measurement wave: set PASTURE_RUN_LOAD_TESTS=1")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")
	profile := measurementProfile()
	bootstrap(t, dbPath, profile)
	uncontended := runWorkers(t, dbPath, dir, profile, 1, deliveriesPerWriter)
	contended := runWorkers(t, dbPath, dir, profile, loadWriters, deliveriesPerWriter)
	u99, c99 := p99(uncontended.Durations), p99(contended.Durations)
	t.Logf("occurrence commit: uncontended p99=%s, contended p99=%s, ratio=%.2f, deliveries=%d, contention failures=%d, deadline failures=%d, storage failures=%d", u99, c99, float64(c99)/float64(u99), loadWriters*deliveriesPerWriter, contended.ContentionFailures, contended.DeadlineFailures, contended.StorageFailures)
	if uncontended.ContentionFailures+uncontended.DeadlineFailures+uncontended.StorageFailures != 0 || contended.ContentionFailures+contended.DeadlineFailures+contended.StorageFailures != 0 {
		t.Fatalf("delivery failures: uncontended contention=%d deadline=%d storage=%d; contended contention=%d deadline=%d storage=%d; every delivery must produce a receipt", uncontended.ContentionFailures, uncontended.DeadlineFailures, uncontended.StorageFailures, contended.ContentionFailures, contended.DeadlineFailures, contended.StorageFailures)
	}
	want := loadWriters * deliveriesPerWriter
	tracker, err := openTracker(dbPath, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	if err := tasks.RebuildLifecycleOccurrences(context.Background(), tracker); err != nil {
		t.Fatal(err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	query := model.OccurrenceQuery{Events: []model.ContractEventKind{registration.EventSessionStart}, Page: model.PageRequest{Size: model.MaxPageSize}}
	for {
		page, err := reader.Occurrences(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		count += len(page.Records())
		if page.State.Next == nil {
			break
		}
		query.Page.Cursor = page.State.Next
	}
	if count != want+deliveriesPerWriter {
		t.Fatalf("public reader count=%d, want %d; dropped receipts are forbidden", count, want+deliveriesPerWriter)
	}
	if c99 > time.Duration(maxP99Ratio)*u99 {
		t.Fatalf("occurrence-commit p99 ratio %.2f exceeds observational bound %d (uncontended=%s contended=%s)", float64(c99)/float64(u99), maxP99Ratio, u99, c99)
	}
}

func TestSliceStartHonestFailureUnderInjectedDelay(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")
	profile := timeouts.DeadlineTestProfile()
	bootstrap(t, dbPath, profile)
	tracker, err := openTrackerWithPool(dbPath, profile, 2)
	if err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	lockErr := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { lockErr <- budget.HoldWriter(ctx, dbPath, timeouts.DeadlineTestProfile(), held, release) }()
	<-held
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, budget.RealClock{}, &operationSource{prefix: "honest"}, profile)
	if err != nil {
		close(release)
		tracker.Close()
		t.Fatal(err)
	}
	realStore, ok := service.Blobs.(receipt.SQLiteBlobStore)
	if !ok {
		close(release)
		tracker.Close()
		t.Fatalf("production blob store type=%T", service.Blobs)
	}
	// Keep the constructor-wired journal. The probe occupies one connection
	// from its shared blob/journal pool so ApplyContext uses the profile-configured
	// second connection instead of the activation connection's longer timeout.
	probeErr := make(chan error, 1)
	go func() {
		probeErr <- realStore.Put(ctx, digest.FromString("pasture.budget.contention-probe"), []byte("contention probe"))
	}()
	if err := waitForStoreConnection(ctx, realStore); err != nil {
		close(release)
		tracker.Close()
		t.Fatal(err)
	}
	// Blob storage is outside the occurrence lock-hold budget. Treat this body
	// as already content-addressed so the injected contention targets only the
	// second transaction.
	service.Blobs = existingBlob{}
	raw, err := os.ReadFile(filepath.Join("..", "..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	if err != nil {
		t.Fatal(err)
	}
	delivery := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], registration.ClaudeCode2_1_210().Version, model.OccurrenceEnvelopeRef{}).Delivery
	_, err = service.Receive(context.Background(), deliveryWarrant(t, delivery), delivery)
	if probe := <-probeErr; probe == nil {
		close(release)
		tracker.Close()
		t.Fatal("contention probe unexpectedly committed while writer lock was held")
	}
	close(release)
	if lock := <-lockErr; lock != nil {
		t.Fatal(lock)
	}
	tracker.Close()
	var contention model.IngressContentionError
	if !stderrors.As(err, &contention) {
		t.Fatalf("Receive error=%v, want typed IngressContentionError", err)
	}
	var deadline model.IngressDeadlineError
	if stderrors.As(err, &deadline) {
		t.Fatalf("Receive error=%v, unexpectedly classified as IngressDeadlineError", err)
	}
	var sqliteErr *sqlite.Error
	if !stderrors.As(err, &sqliteErr) {
		t.Fatalf("Receive error=%v, want underlying *sqlite.Error through Unwrap", err)
	}
	if sqliteErr.Code() != 5 {
		t.Fatalf("underlying SQLite code=%d, want base SQLITE_BUSY code 5", sqliteErr.Code())
	}
	tracker, err = openTracker(dbPath, timeouts.DeadlineTestProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	if err := tasks.RebuildLifecycleOccurrences(context.Background(), tracker); err != nil {
		t.Fatal(err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader.Occurrences(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records()) != 0 {
		t.Fatalf("contention-failed delivery exposed %d receipts, want zero", len(page.Records()))
	}
}

func TestBlobFirstFailureLeavesReclaimableOrphanWithoutReceipt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")
	profile := timeouts.DeadlineTestProfile()
	bootstrap(t, dbPath, profile)
	tracker, err := openTrackerWithPool(dbPath, profile, 2)
	if err != nil {
		t.Fatal(err)
	}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, budget.RealClock{}, &operationSource{prefix: "orphan"}, profile)
	if err != nil {
		tracker.Close()
		t.Fatal(err)
	}
	realStore, ok := service.Blobs.(receipt.SQLiteBlobStore)
	if !ok {
		tracker.Close()
		t.Fatalf("production blob store type=%T", service.Blobs)
	}
	held, release, lockErr := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	probeErr := make(chan error, 1)
	service.Blobs = lockAfterBlob{store: realStore, dbPath: dbPath, profile: profile, held: held, release: release, lockErr: lockErr, probeErr: probeErr}
	raw, err := os.ReadFile(filepath.Join("..", "..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	if err != nil {
		t.Fatal(err)
	}
	delivery := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], registration.ClaudeCode2_1_210().Version, model.OccurrenceEnvelopeRef{}).Delivery
	ref := digest.FromBytes(delivery.Body)
	_, receiveErr := service.Receive(context.Background(), deliveryWarrant(t, delivery), delivery)
	if probe := <-probeErr; probe == nil {
		close(release)
		tracker.Close()
		t.Fatal("contention probe unexpectedly committed while writer lock was held")
	}
	close(release)
	if err := <-lockErr; err != nil {
		tracker.Close()
		t.Fatal(err)
	}
	if receiveErr == nil {
		tracker.Close()
		t.Fatal("occurrence commit succeeded under injected lock")
	}
	var contention model.IngressContentionError
	if !stderrors.As(receiveErr, &contention) {
		tracker.Close()
		t.Fatalf("Receive error=%v, want typed IngressContentionError", receiveErr)
	}
	var deadline model.IngressDeadlineError
	if stderrors.As(receiveErr, &deadline) {
		tracker.Close()
		t.Fatalf("Receive error=%v, unexpectedly classified as IngressDeadlineError", receiveErr)
	}
	var sqliteErr *sqlite.Error
	if !stderrors.As(receiveErr, &sqliteErr) {
		tracker.Close()
		t.Fatalf("Receive error=%v, want underlying *sqlite.Error through Unwrap", receiveErr)
	}
	if sqliteErr.Code() != 5 {
		tracker.Close()
		t.Fatalf("underlying SQLite code=%d, want base SQLITE_BUSY code 5", sqliteErr.Code())
	}
	exists, err := realStore.Exists(context.Background(), ref)
	if err != nil || !exists {
		tracker.Close()
		t.Fatalf("blob exists=%v err=%v, want committed blob", exists, err)
	}
	orphans, err := realStore.Reclaimable(context.Background(), receipt.MaxReclaimablePayloads)
	if err != nil {
		tracker.Close()
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0] != ref {
		tracker.Close()
		t.Fatalf("reclaimable set = %v, want exactly [%s]", orphans, ref)
	}
	tracker.Close()
	tracker, err = openTracker(dbPath, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	if err := tasks.RebuildLifecycleOccurrences(context.Background(), tracker); err != nil {
		t.Fatal(err)
	}
	reader, err := tasks.NewLifecycleReader(tracker)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader.Occurrences(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records()) != 0 {
		t.Fatalf("public reader exposed %d receipts for orphaned blob", len(page.Records()))
	}
}

func TestBudgetWorkerProcess(t *testing.T) {
	dbPath := os.Getenv("PASTURE_BUDGET_DB")
	if dbPath == "" {
		return
	}
	count, _ := strconv.Atoi(os.Getenv("PASTURE_BUDGET_COUNT"))
	resultPath := os.Getenv("PASTURE_BUDGET_RESULT")
	readyPath := os.Getenv("PASTURE_BUDGET_READY")
	startPath := os.Getenv("PASTURE_BUDGET_START")
	prefix := filepath.Base(resultPath)
	profile := timeouts.TestProfile()
	if os.Getenv("PASTURE_BUDGET_PROFILE") == "production" {
		profile = timeouts.ProductionProfile()
	}
	tracker, err := openTracker(dbPath, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	clock := budget.RealClock{}
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, clock, &operationSource{prefix: prefix}, profile)
	if err != nil {
		t.Fatal(err)
	}
	result := observation{}
	service.Appender.Observer = observer{result: &result}
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(startPath); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	if err != nil {
		t.Fatal(err)
	}
	delivery := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], registration.ClaudeCode2_1_210().Version, model.OccurrenceEnvelopeRef{}).Delivery
	warrant := deliveryWarrant(t, delivery)
	for i := 0; i < count; i++ {
		if _, err := service.Receive(context.Background(), warrant, delivery); err != nil {
			var contention model.IngressContentionError
			if stderrors.As(err, &contention) {
				result.ContentionFailures++
				continue
			}
			var deadline model.IngressDeadlineError
			if stderrors.As(err, &deadline) {
				result.DeadlineFailures++
				continue
			}
			result.StorageFailures++
			continue
		}
	}
	body, _ := json.Marshal(result)
	if err := os.WriteFile(resultPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// deliveryWarrant builds a valid delivery-receipt warrant for a parsed budget
// delivery. Receive requires a delivery-receipt warrant; the gate certifies the
// write class, so this covers every delivery in the budget load tests.
func deliveryWarrant(t *testing.T, delivery receipt.Delivery) gate.Warrant {
	t.Helper()
	intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	if refusal != nil {
		t.Fatalf("build delivery intent: %v", refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		t.Fatalf("legalize delivery intent: %v", refusal)
	}
	return warrant
}

func bootstrap(t *testing.T, dbPath string, profile timeouts.Profile) {
	t.Helper()
	tracker, err := openTracker(dbPath, profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tracker.Create("budget", "bootstrap", "initialize receipt identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatal(err)
	}
	tracker.Close()
}

func openTracker(dbPath string, profile timeouts.Profile) (protocol.TaskTracker, error) {
	return openTrackerWithPool(dbPath, profile, 0)
}

func openTrackerWithPool(dbPath string, profile timeouts.Profile, poolSize int) (protocol.TaskTracker, error) {
	options := []tasks.OpenTaskTrackerOption{tasks.WithTimeoutProfile(profile)}
	if poolSize > 0 {
		options = append(options, tasks.WithMaxOpenConns(poolSize))
	}
	return tasks.OpenTaskTrackerWithOptions(dbPath, options...)
}

func waitForStoreConnection(ctx context.Context, store receipt.SQLiteBlobStore) error {
	if store.DB == nil {
		return fmt.Errorf("contention fixture cannot observe the production SQLite pool: nil database handle")
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		if store.DB.Stats().InUse > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("contention fixture could not observe a production SQLite connection before the fixture context ended: %w", ctx.Err())
		case <-timer.C:
			return fmt.Errorf("contention fixture could not observe a production SQLite connection within one second")
		case <-ticker.C:
		}
	}
}

func measurementProfile() timeouts.Profile {
	if os.Getenv("PASTURE_BUDGET_USE_PRODUCTION") == "1" {
		return timeouts.ProductionProfile()
	}
	return timeouts.TestProfile()
}
func runWorkers(t *testing.T, dbPath, dir string, profile timeouts.Profile, workers, count int) observation {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	results := make([]string, workers)
	startPath := filepath.Join(dir, fmt.Sprintf("start-%d", workers))
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		results[i] = filepath.Join(dir, fmt.Sprintf("result-%d-%d.json", workers, i))
		readyPath := filepath.Join(dir, fmt.Sprintf("ready-%d-%d", workers, i))
		wg.Add(1)
		cmd := exec.Command(executable, "-test.run=^TestBudgetWorkerProcess$", "-test.count=1")
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		profileName := "test"
		if profile.Kind() == timeouts.Production {
			profileName = "production"
		}
		cmd.Env = append(os.Environ(), "PASTURE_BUDGET_DB="+dbPath, "PASTURE_BUDGET_COUNT="+strconv.Itoa(count), "PASTURE_BUDGET_RESULT="+results[i], "PASTURE_BUDGET_READY="+readyPath, "PASTURE_BUDGET_START="+startPath, "PASTURE_BUDGET_PROFILE="+profileName)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func(command *exec.Cmd, captured *bytes.Buffer) {
			defer wg.Done()
			if err := command.Wait(); err != nil {
				errs <- fmt.Errorf("worker: %w: %s", err, captured.String())
			}
		}(cmd, &output)
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(readyPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("worker %d did not become ready", i)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := os.WriteFile(startPath, []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	combined := observation{}
	for _, path := range results {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var result observation
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		combined.Durations = append(combined.Durations, result.Durations...)
		combined.ContentionFailures += result.ContentionFailures
		combined.DeadlineFailures += result.DeadlineFailures
		combined.StorageFailures += result.StorageFailures
	}
	return combined
}
func p99(values []int64) time.Duration {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	rank := (99*len(values) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return time.Duration(values[rank-1])
}
