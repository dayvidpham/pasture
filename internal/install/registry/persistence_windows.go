//go:build windows

package registry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func registryRequiresPOSIXMode() bool { return false }

var registryWindowsRename = func(root *os.Root, oldName, newName string) error {
	return root.Rename(oldName, newName)
}

// openRegistryParentWindows anchors traversal at the volume root. os.Root is
// implemented with Windows directory handles and remains confined under
// concurrent filesystem layout changes. Each stable reparse boundary is
// rejected before opening the next rooted directory.
func openRegistryParentWindows(path string) (*os.Root, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	volume := filepath.VolumeName(abs)
	if volume == "" {
		return nil, "", fmt.Errorf("registry path %q has no Windows volume", path)
	}
	root, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(volume+string(filepath.Separator), filepath.Dir(abs))
	if err != nil {
		root.Close()
		return nil, "", err
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		info, statErr := root.Lstat(part)
		if statErr != nil {
			root.Close()
			return nil, "", statErr
		}
		if isWindowsReparse(info) || !info.IsDir() {
			root.Close()
			return nil, "", fmt.Errorf("parent component %q is a reparse link or non-directory", part)
		}
		next, openErr := root.OpenRoot(part)
		if openErr != nil {
			root.Close()
			return nil, "", openErr
		}
		opened, openedErr := next.Lstat(".")
		if openedErr != nil || !os.SameFile(info, opened) {
			next.Close()
			root.Close()
			return nil, "", fmt.Errorf("parent component %q changed while opening its directory handle", part)
		}
		root.Close()
		root = next
	}
	return root, filepath.Base(abs), nil
}

func readRegistryFile(path string) ([]byte, os.FileInfo, error) {
	root, base, err := openRegistryParentWindows(path)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	entry, err := root.Lstat(base)
	if err != nil {
		return nil, nil, err
	}
	if isWindowsReparse(entry) {
		return nil, nil, fmt.Errorf("registry path is a reparse link")
	}
	file, err := root.Open(base)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(entry, info) {
		return nil, nil, fmt.Errorf("registry entry changed while opening its file handle")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("registry descriptor must be a regular file; got %s", info.Mode().Type())
	}
	if err := validateOwnerOnlyWindows(windows.Handle(file.Fd())); err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRegistryBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxRegistryBytes {
		return nil, nil, fmt.Errorf("registry exceeds the %d-byte input limit", maxRegistryBytes)
	}
	return data, info, nil
}

func writeRegistryFile(path string, data []byte) error {
	root, base, err := openRegistryParentWindows(path)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, statErr := root.Lstat(base); statErr == nil {
		if isWindowsReparse(info) || !info.Mode().IsRegular() {
			return fmt.Errorf("registry destination is a reparse link or non-regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	temp := ".pasture-registry-" + hex.EncodeToString(suffix[:])
	parent, err := openNativeWindowsParent(path, root)
	if err != nil {
		return err
	}
	defer parent.Close()
	tempName, err := windows.NewNTUnicodeString(temp)
	if err != nil {
		return err
	}
	_, descriptor, _, err := currentWindowsOwnerACL()
	if err != nil {
		return fmt.Errorf("construct owner-only registry security descriptor: %w", err)
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         tempName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: descriptor,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, 0, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), temp)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return fmt.Errorf("convert secured registry temporary handle to file")
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(temp)
		}
	}()
	if err := validateOwnerOnlyWindows(windows.Handle(file.Fd())); err != nil {
		file.Close()
		return err
	}
	tempEntry, err := root.Lstat(temp)
	if err != nil {
		file.Close()
		return fmt.Errorf("verify secured registry temporary file in rooted parent: %w", err)
	}
	tempInfo, err := file.Stat()
	if err != nil || isWindowsReparse(tempEntry) || !os.SameFile(tempEntry, tempInfo) {
		file.Close()
		return fmt.Errorf("secured registry temporary file changed parent or became a reparse point")
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := registryWindowsRename(root, temp, base); err != nil {
		return err
	}
	committed = true
	// Windows documents FlushFileBuffers for file handles, not directory
	// handles. The temp file is flushed before the atomic rooted rename; do not
	// turn a successful committed replacement into a false failure afterward.
	return nil
}

func openNativeWindowsParent(path string, root *os.Root) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), filepath.Dir(abs))
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("convert rooted registry parent handle to file")
	}
	rootInfo, rootErr := root.Lstat(".")
	fileInfo, fileErr := file.Stat()
	if rootErr != nil || fileErr != nil || isWindowsReparse(fileInfo) || !os.SameFile(rootInfo, fileInfo) {
		file.Close()
		return nil, fmt.Errorf("registry parent changed or became a reparse point while opening its native handle")
	}
	return file, nil
}

func currentWindowsOwnerACL() (*windows.SID, *windows.SECURITY_DESCRIPTOR, *windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, nil, err
	}
	// NtCreateFile receives both the explicit owner and the protected DACL, so
	// token-default ownership cannot differ from the identity validation expects.
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, nil, nil, err
	}
	dacl, _, err := sd.DACL()
	return user.User.Sid, sd, dacl, err
}

func validateOwnerOnlyWindows(handle windows.Handle) error {
	want, _, _, err := currentWindowsOwnerACL()
	if err != nil {
		return fmt.Errorf("resolve registry owner SID: %w", err)
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read registry security descriptor: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(want) {
		return fmt.Errorf("registry owner is not the current user")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("registry DACL inherits access instead of being protected owner-only access")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("registry DACL is not protected owner-only access")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return fmt.Errorf("registry DACL does not contain one owner allow entry")
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(want) {
		return fmt.Errorf("registry DACL grants access to a non-owner identity")
	}
	if ace.Mask != windows.GENERIC_ALL {
		return fmt.Errorf("registry owner DACL does not grant the expected owner-only full access")
	}
	return nil
}

func isWindowsReparse(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || (ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0)
}
