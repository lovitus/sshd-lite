package main

import (
	"golang.org/x/sys/windows"
	"os"
	"testing"
)

func setTestCredentialDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}
func testUserSID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}
func secureTestCredentialFile(t *testing.T, path string) {
	t.Helper()
	setTestCredentialDACL(t, path, "D:P(A;;FA;;;"+testUserSID(t)+")")
}
func TestWindowsCredentialACL(t *testing.T) {
	user := testUserSID(t)
	for _, tc := range []struct {
		name, dacl string
		allowed    bool
	}{
		{"owner-only", "D:P(A;;FA;;;" + user + ")", true},
		{"system-admin", "D:P(A;;FA;;;" + user + ")(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"everyone-read", "D:P(A;;FA;;;" + user + ")(A;;FR;;;WD)", false},
		{"users-write", "D:P(A;;FA;;;" + user + ")(A;;FW;;;BU)", false},
		{"authenticated-read", "D:P(A;;FA;;;" + user + ")(A;;FR;;;AU)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := credentialFile(t, "alice:secret")
			setTestCredentialDACL(t, p, tc.dacl)
			_, err := readCredentialFile(p)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v: %v", tc.allowed, err)
			}
		})
	}
	t.Run("null-dacl", func(t *testing.T) {
		p := credentialFile(t, "alice:secret")
		defer secureTestCredentialFile(t, p)
		if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := readCredentialFile(p); err == nil {
			t.Fatal("null DACL accepted")
		}
	})
	if _, err := readCredentialFile(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	p := credentialFile(t, "alice:secret")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredentialFile(p); err == nil {
		t.Fatal("missing file accepted")
	}
}
