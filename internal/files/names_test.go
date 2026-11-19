package files

import (
	"strings"
	"testing"
)

func TestCleanName(t *testing.T) {
	for name, want := range map[string]string{
		"photo.png":                       "photo.png",
		`C:\Users\ana\Desktop\cv.pdf`:     "cv.pdf",
		"../../etc/passwd":                "passwd",
		"  informe final.pdf ":            "informe final.pdf",
		"line\nbreak\x00.png":             "file", // not valid text
		"tab\there.png":                   "tabhere.png",
		"":                                "file",
		"..":                              "file",
		"folder/":                         "file",
		strings.Repeat("a", 300) + ".png": strings.Repeat("a", 196) + ".png",
	} {
		if got := cleanName(name); got != want {
			t.Errorf("cleanName(%.30q) = %.30q (%d), want %.30q", name, got, len(got), want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for bytes, want := range map[int64]string{10 << 20: "10 MB", 5 << 20: "5 MB", 1<<20 + 1<<19: "1.5 MB", 512 << 10: "512 KB"} {
		if got := humanSize(bytes); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", bytes, got, want)
		}
	}
}
