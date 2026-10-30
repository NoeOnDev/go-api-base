package auth

import (
	_ "embed"
	"strings"
	"sync"
)

// passwords/common.txt holds the 10,000 most common passwords of 8 to 128 characters, in lower
// case and one per line. They come from the 100k-most-used-passwords-NCSC.txt list of SecLists
// (MIT license, in passwords/LICENSE), built from real breaches. ASVS (6.2.4) asks to reject at
// least the 3,000 most common ones that meet the length rule.
//
//go:embed passwords/common.txt
var commonPasswordsFile string

// commonPasswords is the list as a set. It is built the first time a password is checked.
var commonPasswords = sync.OnceValue(func() map[string]struct{} {
	set := make(map[string]struct{}, 10_000)
	for p := range strings.Lines(commonPasswordsFile) {
		set[strings.TrimSuffix(p, "\n")] = struct{}{}
	}
	return set
})

// isCommonPassword reports whether the password is in the list, ignoring letter case:
// "Password1" is as easy to guess as "password1".
func isCommonPassword(password string) bool {
	_, ok := commonPasswords()[strings.ToLower(password)]
	return ok
}
