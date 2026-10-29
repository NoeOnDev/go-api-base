package auth_test

import (
	"slices"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
)

// Los correos de la cola cifrados con una clave anterior impiden quitarla; los de la clave
// actual no cuentan.
func TestKeyReportAddQueuedEmails(t *testing.T) {
	report := auth.KeyReport{CurrentKeyID: "actual", RemovableKeys: []string{"vieja-1", "vieja-2"}}
	if !report.Done() {
		t.Fatal("sin nada pendiente, el reporte debería estar listo")
	}

	report.AddQueuedEmails(map[string]int64{"actual": 4, "vieja-2": 3, "vieja-3": 0})

	if want := []auth.KeyCount{{KeyID: "vieja-2", Count: 3}}; !slices.Equal(report.QueuedEmails, want) {
		t.Errorf("QueuedEmails = %+v, se esperaba %+v", report.QueuedEmails, want)
	}
	if want := []string{"vieja-1"}; !slices.Equal(report.RemovableKeys, want) {
		t.Errorf("RemovableKeys = %v, se esperaba %v", report.RemovableKeys, want)
	}
	if report.Done() {
		t.Error("con correos en la cola, la rotación no debería darse por terminada")
	}
}
