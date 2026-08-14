//go:build !linux && !darwin && !windows

package registry

import (
	"fmt"
	"os"
)

func readRegistryFile(string) ([]byte, os.FileInfo, error) {
	return nil, nil, fmt.Errorf("registry persistence is supported only on Linux, Darwin, and Windows")
}

func writeRegistryFile(string, []byte) error {
	return fmt.Errorf("registry persistence is supported only on Linux, Darwin, and Windows")
}
