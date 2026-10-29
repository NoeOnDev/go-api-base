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
		{"correct test database", "postgres://u:p@localhost:5432/mi_app_test", app, false},
		{"without DATABASE_URL", "postgres://u:p@localhost:5432/mi_app_test", "", false},
		{"same database as the app", app, app, true},
		{"name without _test", "postgres://u:p@localhost:5432/mi_app_dev", app, true},
		{"production by mistake", "postgres://u:p@db.client.com:5432/produccion", app, true},
		{"same _test database as the app", "postgres://u:p@localhost:5432/x_test", "postgres://other:secret@localhost:5432/x_test", true},
		{"invalid URL", "://", app, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkTestDatabase(tt.testURL, tt.appURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, want an error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestRequireTestDatabase(t *testing.T) {
	for env, want := range map[string]bool{"": false, "true": true, "1": true, "false": false, "cualquiera": false} {
		getenv := func(string) string { return env }
		if got := requireTestDatabase(getenv); got != want {
			t.Errorf("CI=%q: requireTestDatabase = %v, want %v", env, got, want)
		}
	}
}
