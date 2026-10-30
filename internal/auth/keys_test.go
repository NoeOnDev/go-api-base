package auth_test

import (
	"slices"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
)

// The queued emails encrypted with a previous key prevent removing it; those of the current key
// do not count.
func TestKeyReportAddQueuedEmails(t *testing.T) {
	report := auth.KeyReport{CurrentKeyID: "current", RemovableKeys: []string{"old-1", "old-2"}}
	if !report.Done() {
		t.Fatal("with nothing pending, the report should be done")
	}

	report.AddQueuedEmails(map[string]int64{"current": 4, "old-2": 3, "old-3": 0})

	if want := []auth.KeyCount{{KeyID: "old-2", Count: 3}}; !slices.Equal(report.QueuedEmails, want) {
		t.Errorf("QueuedEmails = %+v, want %+v", report.QueuedEmails, want)
	}
	if want := []string{"old-1"}; !slices.Equal(report.RemovableKeys, want) {
		t.Errorf("RemovableKeys = %v, want %v", report.RemovableKeys, want)
	}
	if report.Done() {
		t.Error("with emails in the queue, the rotation should not be done")
	}
}
