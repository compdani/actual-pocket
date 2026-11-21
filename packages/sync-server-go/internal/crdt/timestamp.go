package crdt

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	// MaxCounter is the largest counter a timestamp can carry (0xFFFF).
	MaxCounter = 0xFFFF
	// MaxNodeLength is the maximum length of the node id in a timestamp.
	MaxNodeLength = 16
)

// Timestamp is the parsed form of a HULC timestamp such as
// "2015-04-24T22:23:42.123Z-1000-0123456789ABCDEF".
//
// It is a port of packages/crdt/src/crdt/timestamp.ts, limited to what the
// server needs: parsing, formatting and hashing.
type Timestamp struct {
	millis  int64
	counter int
	node    string
}

// Parse converts a timestamp string into a Timestamp. It reports false for
// anything Date.parse rejects, a counter above 0xFFFF, or a node longer than
// 16 UTF-16 code units - the same checks Timestamp.parse applies.
func Parse(s string) (*Timestamp, bool) {
	// The timestamp is date "T" time "Z" "-" counter "-" node. Splitting on
	// "-" yields five parts because the ISO date itself only has dashes.
	parts := strings.Split(s, "-")
	if len(parts) != 5 {
		return nil, false
	}

	millis, ok := parseJSDate(strings.Join(parts[:3], "-"))
	if !ok || millis < 0 {
		return nil, false
	}

	counter, ok := parseInt(parts[3], 16)
	if !ok {
		return nil, false
	}

	node := parts[4]
	if len(utf16.Encode([]rune(node))) > MaxNodeLength {
		return nil, false
	}

	return &Timestamp{millis: millis, counter: counter, node: node}, true
}

// Millis is the logical wall time in milliseconds.
func (t *Timestamp) Millis() int64 { return t.millis }

// Counter is the sequence number within the millisecond.
func (t *Timestamp) Counter() int { return t.counter }

// Node is the id of the client that generated the timestamp.
func (t *Timestamp) Node() string { return t.node }

// String re-formats the parsed state; it never returns the original input.
// That matters because Hash() hashes this value.
func (t *Timestamp) String() string {
	formatted := time.UnixMilli(t.millis).UTC().Format("2006-01-02T15:04:05.000Z")

	counter := leftPad4(hexUpper(t.counter))
	node := padNode(t.node)

	return formatted + "-" + counter + "-" + node
}

// padNode mirrors ('0000000000000000' + node).slice(-16): pad on the left and
// keep the last 16 UTF-16 code units (JavaScript measures strings that way).
func padNode(node string) string {
	units := utf16.Encode([]rune("0000000000000000" + node))
	if len(units) > MaxNodeLength {
		units = units[len(units)-MaxNodeLength:]
	}
	return string(utf16.Decode(units))
}

// Hash is the murmur3 hash of String(), narrowed to the signed int32 the JS
// merkle trie stores (JS `^` converts the unsigned hash to int32).
func (t *Timestamp) Hash() int32 {
	return int32(Murmur3String(t.String()))
}

// hexUpper mirrors Number#toString(16).toUpperCase(). Negative counters keep
// their sign, exactly like JS, even though the result does not round-trip.
func hexUpper(value int) string {
	digits := "0123456789ABCDEF"
	negative := value < 0
	if negative {
		value = -value
	}
	if value == 0 {
		return "0"
	}

	var out [16]byte
	i := len(out)
	for value > 0 {
		i--
		out[i] = digits[value%16]
		value /= 16
	}
	if negative {
		i--
		out[i] = '-'
	}

	return string(out[i:])
}

// leftPad4 mirrors ('0000' + s).slice(-4).
func leftPad4(s string) string {
	padded := "0000" + s
	return padded[len(padded)-4:]
}

// parseInt mimics JavaScript's parseInt(s, base) for base 16: leading
// whitespace and an optional sign are allowed, parsing stops at the first
// invalid digit, and "0x" is accepted as a prefix.
func parseInt(s string, base int) (int, bool) {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	if s == "" {
		return 0, false
	}

	negative := false
	switch s[0] {
	case '-':
		negative = true
		s = s[1:]
	case '+':
		s = s[1:]
	}

	if base == 16 && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}

	value := 0
	digits := 0
	for _, r := range s {
		digit := -1
		switch {
		case r >= '0' && r <= '9':
			digit = int(r - '0')
		case r >= 'a' && r <= 'f':
			digit = int(r-'a') + 10
		case r >= 'A' && r <= 'F':
			digit = int(r-'A') + 10
		}
		if digit < 0 {
			break
		}
		value = value*base + digit
		if value > MaxCounter {
			return 0, false
		}
		digits++
	}

	if digits == 0 {
		return 0, false
	}
	if negative {
		value = -value
	}

	return value, true
}

// isoDatePattern is the ISO 8601 subset Date.parse accepts for our values.
// The zone may be Z, ±HH:MM or ±HHMM; the separator may be T, t or a space.
var isoDatePattern = regexp.MustCompile(
	`^(\d{4})-(\d{2})-(\d{2})` +
		`(?:[Tt ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?` +
		`([Zz]|[+-]\d{2}:?\d{2})?)?$`,
)

// parseJSDate converts the date part of a timestamp to Unix milliseconds.
//
// ECMAScript's Date.parse is more forgiving than Go's time.Parse: it rolls an
// overflowing day into the next month (February 30th is March 1st), accepts
// 24:00:00.000 and truncates extra fractional digits. This reproduces those
// rules for the ISO forms clients produce. A zone-less date-time is read in
// the local time zone, exactly like JavaScript, and a date-only value is UTC.
func parseJSDate(s string) (int64, bool) {
	match := isoDatePattern.FindStringSubmatch(s)
	if match == nil {
		return 0, false
	}

	year := atoi(match[1])
	month := atoi(match[2])
	day := atoi(match[3])

	// Months and days must be in range before normalisation; 1-31 is accepted
	// and then rolled over, matching Date.parse.
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, false
	}

	dateOnly := match[4] == ""
	hour, minute, second, nano := 0, 0, 0, 0
	if !dateOnly {
		hour = atoi(match[4])
		minute = atoi(match[5])
		if match[6] != "" {
			second = atoi(match[6])
		}
		if match[7] != "" {
			digits := match[7]
			if len(digits) > 9 {
				digits = digits[:9]
			}
			digits += strings.Repeat("0", 9-len(digits))
			nano = atoi(digits)
		}

		if hour > 24 || minute > 59 || second > 59 {
			return 0, false
		}
		// 24:00:00.000 is the only valid "hour 24" value.
		if hour == 24 && (minute != 0 || second != 0 || nano != 0) {
			return 0, false
		}
	}

	location := time.Local
	zone := match[8]
	switch {
	case dateOnly, zone == "Z", zone == "z":
		location = time.UTC
	case zone != "":
		offset, ok := parseZoneOffset(zone)
		if !ok {
			return 0, false
		}
		location = time.FixedZone("", offset)
	}

	// time.Date normalises day overflow, which is what MakeDay does in V8.
	parsed := time.Date(year, time.Month(month), day, hour, minute, second, nano, location)

	return parsed.UnixMilli(), true
}

// parseZoneOffset converts ±HH:MM or ±HHMM to seconds east of UTC.
func parseZoneOffset(zone string) (int, bool) {
	if len(zone) < 5 || (zone[0] != '+' && zone[0] != '-') {
		return 0, false
	}

	var hours, minutes int
	switch {
	case len(zone) == 6 && zone[3] == ':':
		hours = atoi(zone[1:3])
		minutes = atoi(zone[4:6])
	case len(zone) == 5:
		hours = atoi(zone[1:3])
		minutes = atoi(zone[3:5])
	default:
		return 0, false
	}
	if hours > 23 || minutes > 59 {
		return 0, false
	}

	offset := hours*3600 + minutes*60
	if zone[0] == '-' {
		offset = -offset
	}
	return offset, true
}

// atoi parses ASCII digits, or returns 0 for anything unexpected. Callers
// validate the range afterwards.
func atoi(s string) int {
	value := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		value = value*10 + int(s[i]-'0')
	}
	return value
}
