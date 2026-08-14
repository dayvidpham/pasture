package registry

import (
	"fmt"
	"os"
)

// Load reads the authoritative registry. Missing means an empty first-shipped
// store. Existing files must be regular, non-symlink, and exactly mode 0600.
func Load(path string) (Store, error) {
	data, info, err := readRegistryFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return Store{}, fault("registry load", "symlink-safe readable registry", fmt.Sprintf("cannot open %q without following links: %v", path, err), path, "opening registry state through a no-follow descriptor", "installation ownership cannot be loaded safely", "replace any symlink with a regular mode-0600 registry file and retry", err)
	}
	if !info.Mode().IsRegular() {
		return Store{}, fault("registry load", "regular non-symlink registry file", fmt.Sprintf("%q has unsafe type %s", path, info.Mode().Type()), path, "checking registry safety before read", "a special file could redirect or fabricate ownership", "replace it with a regular mode-0600 file", nil)
	}
	if info.Mode().Perm() != 0o600 {
		return Store{}, fault("registry load", "mode-0600 registry file", fmt.Sprintf("%q has mode %04o", path, info.Mode().Perm()), path, "checking registry permissions before read", "group or other users could read or alter installation ownership", "run chmod 0600 on the registry after verifying its contents", nil)
	}
	return Parse(data)
}

// Save validates and atomically replaces the registry at mode 0600. The
// registry-specific platform implementation anchors every parent boundary,
// rejects links and non-regular destinations, creates an exclusive
// same-directory temporary file, fsyncs it, renames, and fsyncs the directory.
func Save(path string, s Store) error {
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	if err := writeRegistryFile(path, data); err != nil {
		return fault("registry save", "atomic mode-0600 write through non-symlink boundaries", fmt.Sprintf("cannot save %q: %v", path, err), path, "persisting registry state", "the prior committed registry remains authoritative unless directory fsync alone failed", "replace symlink boundaries, restore directory write access, and retry", err)
	}
	return nil
}
