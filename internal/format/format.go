// Package format renders sizes and idle durations consistently across the
// CLI report and the interactive UI.
package format

import (
	"fmt"
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
