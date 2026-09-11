package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func checkCredentialFileMode(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("credential file must be a regular file")
	}
	return nil
}

// Windows mode bits do not describe access permissions. Inspect the DACL on
// the opened handle, allowing only this account, SYSTEM and Administrators.
// Unknown ACE forms fail closed rather than approximating their permissions.
func checkCredentialFileAccess(f *os.File) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect credential file ACL: %w", err)
	}
	defer runtime.KeepAlive(sd)
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("inspect credential file owner: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("inspect process identity: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	trusted := func(sid *windows.SID) bool {
		return sid != nil && sid.IsValid() && (sid.Equals(user.User.Sid) || sid.Equals(system) || sid.Equals(admins))
	}
	if !trusted(owner) {
		return fmt.Errorf("credential file owner must be the current user, SYSTEM or Administrators")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("credential file must have a restrictive Windows DACL")
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("inspect credential file ACE: %w", err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue // Denials cannot grant additional access.
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if ace.Mask != 0 && !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
				return fmt.Errorf("credential file ACL grants access outside the current user, SYSTEM and Administrators; restrict its ACL")
			}
		default:
			return fmt.Errorf("credential file ACL contains an unsupported access entry")
		}
	}
	return nil
}
