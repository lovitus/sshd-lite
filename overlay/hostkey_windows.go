package main

import (
	"crypto/rand"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"
)

// Apply the private DACL at creation, before anyone could open an inherited
// permissive handle. Mode 0600 alone does not protect Windows private keys.
func createHostKeyTemp(dir string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return nil, err
	}
	defer runtime.KeepAlive(sd)
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	for {
		name := filepath.Join(dir, ".host-key-"+rand.Text())
		path, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			&sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == windows.ERROR_FILE_EXISTS || err == windows.ERROR_ALREADY_EXISTS {
			continue
		}
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(handle), name), nil
	}
}
