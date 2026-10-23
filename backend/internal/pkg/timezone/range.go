package timezone

import "time"

// ParseRangeBound accepts legacy calendar dates and precise RFC3339 timestamps.
// Dates use the user's timezone; an end date includes that entire calendar day.
// Timestamps are exact bounds of a half-open [start, end) range.
func ParseRangeBound(value, userTZ string, end bool) (time.Time, error) {
	if len(value) == len("2006-01-02") {
		t, err := ParseInUserLocation("2006-01-02", value, userTZ)
		if err == nil && end {
			t = t.AddDate(0, 0, 1)
		}
		return t, err
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	// Keep response date labels and chart buckets in the user's timezone.
	localNow := NowInUserLocation(userTZ)
	return t.In(localNow.Location()), nil
}
