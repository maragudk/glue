package model

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"fmt"
	"time"

	"github.com/justincampbell/timeago"
)

// RFC3339Milli is like time.RFC3339Nano, but with millisecond precision,
// and fractional seconds do not have trailing zeros removed.
// Good for sorting chronologically.
const RFC3339Milli = "2006-01-02T15:04:05.000Z07:00"

// Time is a wrapper around [time.Time] that provides custom marshaling and scanning.
// It's useful especially for storing time in SQLite.
type Time struct {
	T time.Time
}

// String satisfies [fmt.Stringer].
func (t Time) String() string {
	return t.T.UTC().Format(RFC3339Milli)
}

var _ fmt.Stringer = Time{}

// ParseTime according to [RFC3339Milli] and return in UTC.
func ParseTime(v string) (Time, error) {
	t, err := time.Parse(RFC3339Milli, v)
	if err != nil {
		return Time{}, err
	}
	return Time{T: t.UTC()}, nil
}

func (t *Time) Pretty() string {
	if t == nil {
		return ""
	}
	if t.T.IsZero() {
		return "-"
	}
	return t.T.UTC().Format("2006-01-02 15:04:05 MST")
}

func (t *Time) Ago() string {
	return timeago.FromTime(t.T)
}

// Value satisfies [driver.Valuer].
func (t Time) Value() (driver.Value, error) {
	return t.String(), nil
}

var _ driver.Valuer = Time{}

// Scan satisfies [sql.Scanner].
func (t *Time) Scan(src any) error {
	if src == nil {
		return nil
	}

	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("error scanning time, got %+v", src)
	}

	parsedT, err := ParseTime(s)
	if err != nil {
		return err
	}

	t.T = parsedT.T

	return nil
}

var _ sql.Scanner = &Time{}

// MarshalText satisfies [encoding.TextMarshaler].
func (t Time) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

var _ encoding.TextMarshaler = Time{}

// UnmarshalText satisfies [encoding.TextUnmarshaler].
func (t *Time) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	parsedT, err := ParseTime(string(data))
	if err != nil {
		return err
	}

	t.T = parsedT.T

	return nil
}

var _ encoding.TextUnmarshaler = &Time{}
