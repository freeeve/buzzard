package format

import "testing"

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, true},
		{"0", 0, true},
		{"1024", 1024, true},
		{"500K", 500 << 10, true},
		{"1M", 1 << 20, true},
		{"1m", 1 << 20, true},
		{"2G", 2 << 30, true},
		{"nope", 0, false},
		{"-5M", 0, false},
	}
	for _, c := range cases {
		got, err := ParseSize(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestHuman(t *testing.T) {
	if got := Human(4 << 20); got != "4.0 MiB" {
		t.Errorf("Human(4MiB) = %q", got)
	}
	if got := Human(512); got != "512 B" {
		t.Errorf("Human(512) = %q", got)
	}
}
