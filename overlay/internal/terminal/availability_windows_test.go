package terminal

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestLegacyBackendSelection(t *testing.T) {
	for _, tc := range []struct {
		mode         string
		failure      error
		legacy, fail bool
	}{
		{"auto", nil, false, false}, {"auto", windows.ERROR_PROC_NOT_FOUND, true, false},
		{"auto", windows.ERROR_ACCESS_DENIED, false, true}, {"auto", windows.ERROR_MOD_NOT_FOUND, false, true},
		{"conpty", windows.ERROR_PROC_NOT_FOUND, false, true}, {"winpty", nil, true, false},
	} {
		got, err := selectLegacyBackend(tc.mode, func(string) error { return tc.failure })
		if got != tc.legacy || (err != nil) != tc.fail {
			t.Fatalf("%+v: legacy=%v err=%v", tc, got, err)
		}
	}
}
