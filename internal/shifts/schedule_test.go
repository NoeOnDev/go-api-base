package shifts

import (
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

func TestSplitByDay(t *testing.T) {
	bogota, _ := civil.LoadLocation("America/Bogota")
	local := func(d civil.Date, h, m int, loc *time.Location) time.Time { return d.At(civil.NewTime(h, m)).In(loc) }
	mon, tue, wed := civil.NewDate(2026, time.November, 2), civil.NewDate(2026, time.November, 3), civil.NewDate(2026, time.November, 4)

	worked := []Worked{
		{Start: local(mon, 8, 0, bogota), End: local(mon, 12, 30, bogota)}, // 270 on Monday
		{Start: local(mon, 22, 0, bogota), End: local(tue, 6, 0, bogota)},  // 120 on Monday, 360 on Tuesday
		{Start: local(wed, 9, 0, bogota)},                                  // open: until now
	}
	now := local(wed, 10, 15, bogota)
	days := SplitByDay(worked, mon, wed, bogota, now)
	want := []int{390, 360, 75}
	for i, d := range days {
		if d.Minutes != want[i] {
			t.Errorf("%s: %d minutes, want %d", d.Date, d.Minutes, want[i])
		}
	}

	// In New York, the night of March 7 to 8, 2026 lasts one hour less: 22:00 to 06:00 is 7 hours, 2 on
	// Saturday and 5 on Sunday (from 00:00 to 06:00 there are only 5 real hours).
	ny, _ := civil.LoadLocation("America/New_York")
	sat, sun := civil.NewDate(2026, time.March, 7), civil.NewDate(2026, time.March, 8)
	night := []Worked{{Start: local(sat, 22, 0, ny), End: local(sun, 6, 0, ny)}}
	days = SplitByDay(night, sat, sun, ny, local(sun, 12, 0, ny))
	if days[0].Minutes != 120 || days[1].Minutes != 300 {
		t.Errorf("the night of the jump: %+v, want 120 and 300 minutes", days)
	}
}
