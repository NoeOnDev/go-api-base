package appointments

import (
	"time"

	"github.com/NoeOnDev/go-api-base/internal/branches"
	"github.com/NoeOnDev/go-api-base/internal/civil"
)

// slotStep is the distance between the starts of two slots: 9:00, 9:15, 9:30…
const slotStep = 15 * time.Minute

// Slot is a time to book: its hour on the branch's clock, and its instants.
type Slot struct {
	LocalStartsAt civil.DateTime `json:"local_starts_at"`
	StartsAt      time.Time      `json:"starts_at"`
	EndsAt        time.Time      `json:"ends_at"`
}

// span is a booked range, in instants.
type span struct {
	start, end time.Time
}

// slots returns the slots of duration that start on date, in loc, every slotStep from the start of
// each opening and ending inside it, that start after now and do not overlap a booked span. The
// openings must include those of the day before date (one that crosses midnight can have slots on
// date).
//
// The steps are of real time: on the day of a daylight saving time change, an opening still has its
// slots every 15 minutes, and none falls in the hour that does not exist.
func slots(openings []branches.Opening, date civil.Date, duration time.Duration, now time.Time, booked []span, loc *time.Location) []Slot {
	free := []Slot{}
	for _, o := range openings {
		for start := o.StartsAt; !start.Add(duration).After(o.EndsAt); start = start.Add(slotStep) {
			local := civil.DateTimeOf(start.In(loc))
			if local.Date() != date || !start.After(now) {
				continue
			}
			s := Slot{LocalStartsAt: local, StartsAt: start.UTC(), EndsAt: start.Add(duration).UTC()}
			if !overlapsAny(s, booked) {
				free = append(free, s)
			}
		}
	}
	return free
}

func overlapsAny(s Slot, booked []span) bool {
	for _, b := range booked {
		if s.StartsAt.Before(b.end) && b.start.Before(s.EndsAt) {
			return true
		}
	}
	return false
}
