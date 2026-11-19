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
// date) and of the day after (a slot can end in an opening that continues the one of date).
// Openings that touch or overlap count as one (branches.Merge): with 18:00-00:00 and 00:00-02:00
// there is a slot of an hour at 23:30, and no slot is repeated.
//
// The steps are of real time: on the day of a daylight saving time change, an opening still has its
// slots every 15 minutes, and none falls in the hour that does not exist. In the hour that happens
// twice, only the first time has slots: a slot is booked by its hour of the clock, which means the
// first of the two (civil.DateTime.In), so the second could never be booked.
func slots(openings []branches.Opening, date civil.Date, duration time.Duration, now time.Time, booked []span, loc *time.Location) []Slot {
	free := []Slot{}
	for _, o := range branches.Merge(openings) {
		for start := o.StartsAt; !start.Add(duration).After(o.EndsAt); start = start.Add(slotStep) {
			local := civil.DateTimeOf(start.In(loc))
			if local.Date() != date || !start.After(now) || !local.In(loc).Equal(start) {
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
