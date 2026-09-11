package legacypty

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

var backend struct {
	sync.Mutex
	dir string
	dll *windows.DLL
}

// A unique process-private directory avoids shared-cache races and stale
// dependencies. Created only for legacy sessions, with its DACL set atomically.
func extract() (string, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("embedded WinPTY is unavailable for %s; ConPTY is required", runtime.GOARCH)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return "", err
	}
	defer runtime.KeepAlive(sd)
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	dir := filepath.Join(os.TempDir(), "sshd-lite-winpty-"+rand.Text())
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	if err := windows.CreateDirectory(path, &sa); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", err
	}
	found := map[string]bool{}
	for _, entry := range archive.File {
		if entry.Name != "winpty.dll" && entry.Name != "winpty-agent.exe" {
			return "", fmt.Errorf("unexpected embedded dependency")
		}
		if found[entry.Name] {
			return "", fmt.Errorf("duplicate embedded dependency")
		}
		found[entry.Name] = true
		in, err := entry.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(filepath.Join(dir, entry.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			in.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if len(found) != 2 {
		return "", fmt.Errorf("incomplete embedded WinPTY payload")
	}
	ok = true
	return dir, nil
}

func load() (*windows.DLL, error) {
	backend.Lock()
	defer backend.Unlock()
	if backend.dll != nil {
		return backend.dll, nil
	}
	dir, err := extract()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "winpty.dll")
	// Use only the private DLL directory and Windows system directory.
	handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("load embedded WinPTY: %w", err)
	}
	backend.dir, backend.dll = dir, &windows.DLL{Name: filepath.Join(dir, "winpty.dll"), Handle: handle}
	log.Print("Using embedded WinPTY terminal backend")
	return backend.dll, nil
}

// Call only after all server sessions have stopped; DLL procedures cannot be
// unloaded while a session owns them. A killed daemon can leave a private temp
// directory; it is never reused or trusted by a subsequent process.
func Cleanup() error {
	backend.Lock()
	defer backend.Unlock()
	if backend.dll == nil {
		return nil
	}
	if err := backend.dll.Release(); err != nil {
		return err
	}
	backend.dll = nil
	err := os.RemoveAll(backend.dir)
	backend.dir = ""
	return err
}
