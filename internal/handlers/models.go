package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/bestiary"
	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/install/fsatomic"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/provenance"
)

const modelRefreshTimeout = 5 * time.Second

type modelAllowance struct {
	Version  int    `json:"version"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type modelSnapshot struct {
	Version   int                  `json:"version"`
	Timestamp string               `json:"timestamp"`
	Models    []bestiary.ModelInfo `json:"models"`
}

type modelCatalog struct {
	allowDir, cachePath string
	fetch               func(context.Context) ([]bestiary.ModelInfo, error)
	now                 func() time.Time
	diagnostics         io.Writer
}

func defaultModelCatalog() modelCatalog {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(filepath.Dir(config.DefaultConfigPath()), "cache")
	}
	return modelCatalog{
		allowDir:  filepath.Join(filepath.Dir(config.DefaultConfigPath()), "models-allowed.d"),
		cachePath: filepath.Join(cache, "pasture", "models-v1.json"),
		fetch:     newModelFetcher(), now: time.Now, diagnostics: os.Stderr,
	}
}

// Options are private dependency injection for local HTTP fixtures, not CLI configuration.
func newModelFetcher(options ...bestiary.ClientOption) func(context.Context) ([]bestiary.ModelInfo, error) {
	options = append(options, bestiary.WithTimeout(modelRefreshTimeout), bestiary.WithRetries(0))
	return bestiary.NewClient(options...).FetchModels
}

func validateModelPair(provider, model string) error {
	if strings.Contains(model, "#") {
		base, _, _ := strings.Cut(model, "#")
		return fmt.Errorf("model registration cannot store execution variants in %q; no identity was created; supply the base pair: pasture models allow --provider %q --model %q (execution variants are not part of author identity)", model, provider, base)
	}
	for _, value := range []string{provider, model} {
		if value == "" || !utf8.ValidString(value) || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return fmt.Errorf("model pair validation: provider and model must be nonempty exact UTF-8 strings without whitespace/control characters; got %q / %q; no identity was created; pass --provider and --model without normalization", provider, model)
		}
	}
	if strings.ContainsAny(provider, "/#") {
		return fmt.Errorf("model pair validation: provider %q contains a slash or hash; no identity was created; pass a separate provider and base --model (inner model slashes are supported)", provider)
	}
	return nil
}

func allowanceName(provider, model string) string {
	b, _ := json.Marshal([2]string{provider, model})
	return fmt.Sprintf("%x.json", sha256.Sum256(b))
}

func decodeModelJSON(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON record")
	}
	return nil
}

func readModelFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (symlinks are refused)")
	}
	return os.ReadFile(path)
}

func modelFileError(phase, path string, err error) error {
	return fmt.Errorf("model catalog %s at %q: %w; registration/cache operation did not complete; inspect this owned file and move malformed data aside, then retry pasture models refresh or pasture models allow --provider <provider> --model <model>. Atomic writes may already be visible if directory fsync failed after rename", phase, path, err)
}

func (c modelCatalog) allow(ctx context.Context, provider, model string) (string, error) {
	if err := validateModelPair(provider, model); err != nil {
		return "", err
	}
	path := filepath.Join(c.allowDir, allowanceName(provider, model))
	if err := ctx.Err(); err != nil {
		return "", modelFileError("allow before write", path, err)
	}
	if err := os.MkdirAll(c.allowDir, 0o700); err != nil {
		return "", modelFileError("allow directory", c.allowDir, err)
	}
	record := modelAllowance{Version: 1, Provider: provider, Model: model}
	if data, err := readModelFile(path); err == nil {
		var old modelAllowance
		if err := decodeModelJSON(data, &old); err != nil {
			return "", modelFileError("allow read", path, err)
		}
		if old != record {
			return "", modelFileError("allow read", path, fmt.Errorf("existing record differs from exact pair"))
		}
	} else if !os.IsNotExist(err) {
		return "", modelFileError("allow read", path, err)
	}
	data, _ := json.Marshal(record)
	if err := ctx.Err(); err != nil {
		return "", modelFileError("allow before publication", path, err)
	}
	if err := fsatomic.WriteFile(path, data, 0o600); err != nil {
		return "", modelFileError("allow write", path, err)
	}
	return path, nil
}

func validSnapshot(s modelSnapshot) error {
	t, err := time.Parse(time.RFC3339, s.Timestamp)
	if err != nil || t.UTC().Format(time.RFC3339) != s.Timestamp {
		return fmt.Errorf("snapshot timestamp must be whole-second RFC3339 UTC")
	}
	if s.Version != 1 {
		return fmt.Errorf("unsupported snapshot version %d", s.Version)
	}
	seen := map[[2]string]bool{}
	for _, m := range s.Models {
		if err := validateModelPair(string(m.Provider), string(m.ID)); err != nil {
			return err
		}
		if m.LastSynced != s.Timestamp {
			return fmt.Errorf("record timestamp differs from snapshot timestamp")
		}
		key := [2]string{string(m.Provider), string(m.ID)}
		if seen[key] {
			return fmt.Errorf("duplicate fetched identity %q", key)
		}
		seen[key] = true
	}
	return nil
}

func (c modelCatalog) registry() (provenance.ModelRegistry, error) {
	var cached []bestiary.ModelInfo
	data, err := readModelFile(c.cachePath)
	if err == nil {
		var s modelSnapshot
		err = decodeModelJSON(data, &s)
		if err == nil {
			err = validSnapshot(s)
		}
		if err == nil {
			cached = s.Models
		}
	}
	if err != nil && !os.IsNotExist(err) && c.diagnostics != nil {
		fmt.Fprintln(c.diagnostics, modelFileError("cache/read (ignored; static and allowed identities remain usable)", c.cachePath, err))
	}
	registry := provenance.RegistryFromBestiary(bestiary.MergeModels(bestiary.Models(), cached))
	entries := registry.Models()
	files, err := os.ReadDir(c.allowDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, modelFileError("allowance/read", c.allowDir, err)
	}
	for _, file := range files {
		// fsatomic crash-orphan temps are not committed allowance records.
		if strings.HasPrefix(file.Name(), ".pasture-tmp-") {
			continue
		}
		path := filepath.Join(c.allowDir, file.Name())
		data, err := readModelFile(path)
		var a modelAllowance
		if err == nil {
			err = decodeModelJSON(data, &a)
		}
		if err == nil {
			err = validateModelPair(a.Provider, a.Model)
		}
		if err == nil && (a.Version != 1 || file.Name() != allowanceName(a.Provider, a.Model)) {
			err = fmt.Errorf("unsupported version or pair hash filename mismatch")
		}
		if err != nil {
			return nil, modelFileError("allowance/read", path, err)
		}
		if _, ok := registry.Lookup(provenance.Provider(a.Provider), a.Model); !ok {
			entries = append(entries, provenance.ModelEntry{Provider: provenance.Provider(a.Provider), Name: provenance.ModelID(a.Model)})
		}
	}
	return provenance.NewRegistry(entries), nil
}

func (c modelCatalog) refresh(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, modelRefreshTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, modelFileError("refresh before fetch", c.cachePath, err)
	}
	models, err := c.fetch(ctx)
	if err != nil {
		return 0, modelFileError("fetch/parse", c.cachePath, err)
	}
	stamp := c.now().UTC().Format(time.RFC3339)
	for i := range models {
		models[i].LastSynced = stamp
	}
	s := modelSnapshot{Version: 1, Timestamp: stamp, Models: models}
	if err := validSnapshot(s); err != nil {
		return 0, modelFileError("fetch validation", c.cachePath, err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return 0, modelFileError("snapshot encode", c.cachePath, err)
	}
	if err := ctx.Err(); err != nil {
		return 0, modelFileError("refresh before publication", c.cachePath, err)
	}
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o700); err != nil {
		return 0, modelFileError("cache directory", c.cachePath, err)
	}
	if err := ctx.Err(); err != nil {
		return 0, modelFileError("refresh before publication", c.cachePath, err)
	}
	if err := fsatomic.WriteFile(c.cachePath, data, 0o600); err != nil {
		return 0, modelFileError("cache publish", c.cachePath, err)
	}
	return len(models), nil
}

func (c modelCatalog) resolve(ctx context.Context, provider, model string) (provenance.ModelRegistry, error) {
	if err := validateModelPair(provider, model); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := c.registry()
	if err != nil {
		return nil, err
	}
	if _, ok := r.Lookup(provenance.Provider(provider), model); ok {
		return r, nil
	}
	if _, err := c.refresh(ctx); err != nil {
		return nil, fmt.Errorf("exact pair %q / %q not admitted; %w", provider, model, err)
	}
	r, err = c.registry()
	if err != nil {
		return nil, err
	}
	if _, ok := r.Lookup(provenance.Provider(provider), model); !ok {
		return nil, fmt.Errorf("effective model registry lookup: exact pair %q / %q remains unknown after refresh; no author created; catalogs do not guarantee coverage; known models for this provider: %s; verify spelling or run pasture models allow --provider %q --model %q", provider, model, listProviderModels(r, provenance.Provider(provider)), provider, model)
	}
	return r, nil
}

// ModelsAllow saves an offline, exact-identity allowance without opening the task store.
func ModelsAllow(ctx context.Context, w io.Writer, provider, model string, format types.OutputFormat) error {
	path, err := defaultModelCatalog().allow(ctx, provider, model)
	if err != nil {
		return err
	}
	if format == types.OutputJSON {
		return json.NewEncoder(w).Encode(struct {
			Operation string `json:"operation"`
			Path      string `json:"path"`
			Provider  string `json:"provider"`
			Model     string `json:"model"`
		}{"allow", path, provider, model})
	}
	_, err = fmt.Fprintf(w, "allowed %s / %s at %s\n", provider, model, path)
	return err
}

// ModelsRefresh publishes an owned Bestiary snapshot without opening the task store.
func ModelsRefresh(ctx context.Context, w io.Writer, format types.OutputFormat) error {
	return modelsRefresh(ctx, w, format, defaultModelCatalog())
}

func modelsRefresh(ctx context.Context, w io.Writer, format types.OutputFormat, c modelCatalog) error {
	n, err := c.refresh(ctx)
	if err != nil {
		return err
	}
	if format == types.OutputJSON {
		return json.NewEncoder(w).Encode(struct {
			Operation  string `json:"operation"`
			Path       string `json:"path"`
			ModelCount int    `json:"model_count"`
		}{"refresh", c.cachePath, n})
	}
	_, err = fmt.Fprintf(w, "refreshed %d models at %s\n", n, c.cachePath)
	return err
}
