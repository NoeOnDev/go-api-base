package db_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestTimeZoneUTC(t *testing.T) {
	pool := testutil.NewDB(t)

	var zone string
	if err := pool.QueryRow(t.Context(), "SHOW TimeZone").Scan(&zone); err != nil {
		t.Fatal(err)
	}
	if zone != "UTC" {
		t.Errorf("TimeZone = %q, want UTC", zone)
	}

	// Whatever the zone of the process, the dates come in UTC, and so does their JSON.
	local := time.Local
	time.Local = time.FixedZone("UTC-6", -6*60*60)
	t.Cleanup(func() { time.Local = local })

	var now time.Time
	if err := pool.QueryRow(t.Context(), "SELECT now()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	if now.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", now.Location())
	}
	b, err := json.Marshal(now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), `Z"`) {
		t.Errorf("JSON = %s, want it to end in Z", b)
	}
}

func TestURLSettingsAreKept(t *testing.T) {
	url := testutil.NewDB(t).Config().ConnString()
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	pool, err := db.Open(t.Context(), url+sep+"timezone=America/Bogota&statement_timeout=30000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var zone, timeout string
	if err := pool.QueryRow(t.Context(), "SELECT current_setting('TimeZone'), current_setting('statement_timeout')").Scan(&zone, &timeout); err != nil {
		t.Fatal(err)
	}
	if zone != "America/Bogota" || timeout != "30s" {
		t.Errorf("TimeZone = %q and statement_timeout = %q, want America/Bogota and 30s", zone, timeout)
	}

	// The session's zone changes what SQL shows, never what Go reads.
	var now time.Time
	if err := pool.QueryRow(t.Context(), "SELECT now()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	if now.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", now.Location())
	}
}
