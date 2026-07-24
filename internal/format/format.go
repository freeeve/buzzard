// Package format renders sizes and idle durations consistently across the
// CLI report and the interactive UI.
package format

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Human renders a byte count with binary prefixes.
func Human(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ParseSize parses a human size: a plain byte count or a binary-prefixed
// one in any usual spelling -- "500K", "1m", "1gb", "2GiB". An empty
// string or "0" means zero.
func ParseSize(s string) (int64, error) {
	orig := s
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}
	s = strings.TrimSuffix(s, "b")
	s = strings.TrimSuffix(s, "i")
	mult := int64(1)
	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'k':
			mult, s = 1<<10, s[:len(s)-1]
		case 'm':
			mult, s = 1<<20, s[:len(s)-1]
		case 'g':
			mult, s = 1<<30, s[:len(s)-1]
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", orig)
	}
	return n * mult, nil
}

// Idle renders how long ago a subtree was last modified.
func Idle(t time.Time) string {
	if t.IsZero() {
		return "(empty)"
	}
	d := time.Since(t)
	switch {
	case d > 365*24*time.Hour:
		return fmt.Sprintf("%.1fy", d.Hours()/(365*24))
	case d > 30*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(30*24)))
	case d > 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return "<1d"
	}
}
