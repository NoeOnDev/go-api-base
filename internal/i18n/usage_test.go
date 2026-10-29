package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The code and the catalogs must match: every error code and key used in the code exists in
// the catalogs, and every catalog key is used somewhere. Error codes also have to be in the
// table of docs/i18n.md, which is what frontend developers read.

var (
	// httpx.Conflict("email_taken", …), NotFound("note_not_found") inside httpx… Any text is
	// captured, so a message written where the code goes fails the test instead of being missed.
	errorHelper = regexp.MustCompile(`\b(?:httpx\.)?(?:BadRequest|NotFound|Unauthorized|Forbidden|Conflict)\("([^"]+)"`)
	// &httpx.Error{…, Code: "too_many_emails"}
	errorLiteral = regexp.MustCompile(`\bCode:\s*"([a-z_]+)"`)
	// i18n.M("validation.required"), t.T("messages.verification_sent"), t.Plural("duration.hours", …)
	// (a key built at run time, like "errors." + code, ends in a dot and is skipped).
	messageKey = regexp.MustCompile(`(?:\bi18n\.M|\.T|\.Plural)\("([a-z_]+(?:\.[a-z_]+)+)"`)
	// NotifyEmailChanged = "email_changed" (the kinds of the account notifications in auth)
	notifyKind = regexp.MustCompile(`\bNotify\w+\s*=\s*"([a-z_.]+)"`)
)

func TestCatalogMatchesCode(t *testing.T) {
	all, err := embedded()
	if err != nil {
		t.Fatal(err)
	}
	base := all[BaseLocale]

	used := map[string]string{} // key → where it is used first
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "store" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			code := string(data)
			add := func(prefix string, re *regexp.Regexp) {
				for _, m := range re.FindAllStringSubmatch(code, -1) {
					if _, ok := used[prefix+m[1]]; !ok {
						used[prefix+m[1]] = path
					}
				}
			}
			add("errors.", errorHelper)
			add("errors.", errorLiteral)
			add("", messageKey)
			add("notifications.", notifyKind)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, key := range sortedKeys(used) {
		if _, ok := base[key]; !ok {
			t.Errorf("%s uses %q, which is not in locales/%s.json", used[key], key, BaseLocale)
		}
	}
	for _, key := range sortedKeys(base) {
		if _, ok := used[key]; !ok {
			t.Errorf("locales/%s.json has %q, but the code never uses it", BaseLocale, key)
		}
	}

	doc, err := os.ReadFile("../../docs/i18n.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range sortedKeys(base) {
		if code, ok := strings.CutPrefix(key, "errors."); ok && !strings.Contains(string(doc), "| `"+code+"` |") {
			t.Errorf("the error code %q is not in the table of docs/i18n.md", code)
		}
	}
}
