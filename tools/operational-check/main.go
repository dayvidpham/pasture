// Command operational-check performs the read-only operational tracker sweep.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
)

func main() {
	identities := flag.Bool("history-report", false, "report exact candidate identities for the supplied source files; never update dispositions")
	flag.Parse()
	if err := run(*identities, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(identities bool, paths []string) error {
	root, err := scan.ModuleRoot()
	if err != nil {
		return err
	}
	if identities {
		var entries []scan.HistoricalTrackerEntry
		for _, file := range paths {
			content, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				return fmt.Errorf("read identity-report source %s: %w; restore the named source and retry", file, err)
			}
			doc := scan.OperationalDocument{Owner: file, File: file, Content: content}
			candidates, err := scan.TrackerCandidates(doc)
			if err != nil {
				return err
			}
			for _, candidate := range candidates {
				entries = append(entries, scan.HistoricalTrackerEntry{
					Owner: file, Section: candidate.Location.Section(), Node: candidate.Node,
					Range: candidate.Location.Range(), Window: candidate.Window, Digest: scan.TrackerDigest(content),
				})
			}
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(entries)
	}
	if err := codegen.RequireClassifiedSource(root); err != nil {
		return err
	}
	for _, dir := range scan.OperationalOutputRoots() {
		if _, err := os.Lstat(filepath.Join(root, dir)); err != nil {
			return fmt.Errorf("operational output sweep cannot inspect required tree %s: %w; output verification withheld; restore canonical inputs and run make generate", dir, err)
		}
	}
	report, err := scan.CheckOperationalTree(root, scan.OperationalOutputRoots(), "CI operational output sweep")
	if err != nil {
		return err
	}
	fmt.Printf("operational tracker sweep: files=%d historical=%d executable=0 tree_sha256=%s\n", report.Files, report.Historical, report.TreeHash)
	return nil
}
