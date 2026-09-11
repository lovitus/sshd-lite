package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	authEnvironment    = "SSHD_LITE_AUTH"
	maxCredentialBytes = 16 * 1024 // Per source, including line endings.
	maxCredentialUsers = 1024      // Across all merged sources.
)

type passwordVerifier func(username string, password []byte) bool

// resolveAuthentication consumes the environment before the server is created.
// Unsetenv prevents ordinary inheritance by subsequently spawned shells; it does
// NOT securely erase strings or the initial environment exposed by Linux /proc.
func resolveAuthentication(auth string) (string, passwordVerifier, error) {
	value, present := os.LookupEnv(authEnvironment)
	if present {
		if err := os.Unsetenv(authEnvironment); err != nil {
			return "", nil, fmt.Errorf("remove authentication environment variable: %w", err)
		}
	}
	return resolveAuthSources(auth, value, present)
}

// resolveAuthSources merges @file (or a legacy CLI pair) with the environment.
// Environment entries replace file/CLI entries for the same, case-sensitive
// username. An explicitly configured invalid source is always a startup error.
func resolveAuthSources(auth, env string, envPresent bool) (string, passwordVerifier, error) {
	var users map[string]string
	var err error
	switch {
	case strings.HasPrefix(auth, "@"):
		users, err = readCredentialFile(strings.TrimPrefix(auth, "@"))
	case envPresent && auth != "":
		// Do not silently mix password records with upstream public-key or
		// unauthenticated modes. Leave those modes unchanged when env is unset.
		if auth == "none" || strings.HasPrefix(auth, "github.com/") || isDrivePath(auth) || !strings.Contains(auth, ":") {
			return "", nil, fmt.Errorf("%s cannot be combined with public-key or no-auth modes; use @file or password records", authEnvironment)
		}
		users, err = parseCredentialRecords(auth, "CLI credentials")
	case envPresent:
		users = make(map[string]string)
	default:
		return auth, nil, nil // Existing upstream authentication modes.
	}
	if err != nil {
		return "", nil, err
	}
	if envPresent {
		envUsers, err := parseCredentialRecords(env, authEnvironment)
		if err != nil {
			return "", nil, err
		}
		for user, password := range envUsers {
			users[user] = password
		}
	}
	if len(users) == 0 || len(users) > maxCredentialUsers {
		return "", nil, fmt.Errorf("merged credentials must contain 1 to %d users", maxCredentialUsers)
	}
	return "", newPasswordVerifier(users), nil
}

func isDrivePath(value string) bool {
	if len(value) < 3 || value[1] != ':' || (value[2] != '/' && value[2] != '\\') {
		return false
	}
	c := value[0]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func readCredentialFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, fmt.Errorf("@ authentication requires a file path")
	}
	// Check before opening to reject ordinary FIFOs/devices without blocking.
	// The containing directory must be trusted; this is not a sandbox against
	// another process able to replace entries in that directory.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect credential file: %w", err)
	}
	if err := checkCredentialFileMode(info); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open credential file: %w", err)
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened credential file: %w", err)
	}
	if err := checkCredentialFileMode(openedInfo); err != nil {
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("credential file changed while opening; retry startup")
	}
	if err := checkCredentialFileAccess(f); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCredentialBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	return parseCredentialRecords(string(data), "credential file")
}

// Records are newline-delimited, with LF or CRLF endings. Split only at the
// first colon; preserve password spaces and punctuation. No comment syntax or
// comma/semicolon splitting, so those characters remain valid password bytes.
func parseCredentialRecords(value, source string) (map[string]string, error) {
	if len(value) > maxCredentialBytes {
		return nil, fmt.Errorf("%s exceeds the %d-byte limit", source, maxCredentialBytes)
	}
	users := make(map[string]string)
	for i, raw := range strings.Split(value, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		user, password, ok := strings.Cut(line, ":")
		if !ok || user == "" || password == "" || strings.ContainsAny(line, "\r\x00") {
			return nil, fmt.Errorf("%s line %d: expected nonempty username:password", source, i+1)
		}
		if !utf8.ValidString(user) || strings.ContainsAny(user, "/\\") || strings.IndexFunc(user, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return nil, fmt.Errorf("%s line %d: invalid virtual username", source, i+1)
		}
		if _, exists := users[user]; exists {
			return nil, fmt.Errorf("%s line %d: duplicate username within this source", source, i+1)
		}
		users[user] = password
		if len(users) > maxCredentialUsers {
			return nil, fmt.Errorf("%s exceeds the %d-user limit", source, maxCredentialUsers)
		}
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("%s contains no username:password records", source)
	}
	return users, nil
}

func newPasswordVerifier(users map[string]string) passwordVerifier {
	// Fixed-size digests allow constant-time password comparison and keep the
	// closure independent of the source map. This is NOT an at-rest password
	// hashing scheme: file and environment records are still plaintext.
	digests := make(map[string][sha256.Size]byte, len(users))
	for user, password := range users {
		digests[user] = sha256.Sum256([]byte(password))
	}
	// Immutable after construction, so simultaneous authentication is safe.
	return func(user string, password []byte) bool {
		want, exists := digests[user]
		got := sha256.Sum256(password)
		match := subtle.ConstantTimeCompare(got[:], want[:])
		return match == 1 && exists
	}
}
