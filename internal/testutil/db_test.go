package testutil

import "testing"

func TestCheckTestDatabase(t *testing.T) {
	const app = "postgres://u:p@localhost:5432/mi_app"

	tests := []struct {
		name    string
		testURL string
		appURL  string
		wantErr bool
	}{
		{"base de pruebas correcta", "postgres://u:p@localhost:5432/mi_app_test", app, false},
		{"sin DATABASE_URL", "postgres://u:p@localhost:5432/mi_app_test", "", false},
		{"misma base que la app", app, app, true},
		{"nombre sin _test", "postgres://u:p@localhost:5432/mi_app_dev", app, true},
		{"producción por error", "postgres://u:p@db.cliente.com:5432/produccion", app, true},
		{"misma base _test que la app", "postgres://u:p@localhost:5432/x_test", "postgres://otro:clave@localhost:5432/x_test", true},
		{"URL inválida", "://", app, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkTestDatabase(tt.testURL, tt.appURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, se esperaba error: %v", err, tt.wantErr)
			}
		})
	}
}
