package rewards

import "time"

// weekdayNames lists the day names in mask order. Bit 0 is Monday and bit 6 is Sunday.
var weekdayNames = [7]string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

const allDaysMask = 1<<7 - 1

// weekdayBit returns the mask bit for a weekday.
func weekdayBit(d time.Weekday) int {
	return 1 << ((int(d) + 6) % 7)
}

// dayBit returns the mask bit for a day name such as "mon".
func dayBit(name string) (int, bool) {
	for i, n := range weekdayNames {
		if n == name {
			return 1 << i, true
		}
	}
	return 0, false
}

// maskOf combines day names into a mask. The names must already be valid.
func maskOf(names []string) int {
	mask := 0
	for _, n := range names {
		if bit, ok := dayBit(n); ok {
			mask |= bit
		}
	}
	return mask
}

// namesFromMask lists the day names in mask, Monday first. A zero mask means every day and
// returns nil.
func namesFromMask(mask int) []string {
	if mask == 0 {
		return nil
	}
	out := []string{}
	for i, n := range weekdayNames {
		if mask&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}

// inWindow reports whether local time falls inside the reward's date range and weekday set.
// The date range includes both ends. local must be in the family's time zone.
func (r Reward) inWindow(local time.Time) bool {
	day := local.Format("2006-01-02")
	if r.AvailableFrom != nil && day < *r.AvailableFrom {
		return false
	}
	if r.AvailableUntil != nil && day > *r.AvailableUntil {
		return false
	}
	if r.days != 0 && r.days&weekdayBit(local.Weekday()) == 0 {
		return false
	}
	return true
}
