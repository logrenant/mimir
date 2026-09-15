package extract_test

import (
	"testing"

	"github.com/logrenant/mimir/internal/extract"
)

func TestParseFloatLoose(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOk bool
	}{
		{"$1,299.99", 1299.99, true},
		{"4.5 out of 5 stars", 4.5, true},
		{"129.99", 129.99, true},
		{"no numbers here", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := extract.ParseFloatLoose(c.in)
		if ok != c.wantOk {
			t.Errorf("ParseFloatLoose(%q) ok = %v, want %v", c.in, ok, c.wantOk)
			continue
		}
		if ok && got != c.want {
			t.Errorf("ParseFloatLoose(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseCompactInt(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOk bool
	}{
		{"12,345", 12345, true},
		{"1.2M", 1200000, true},
		{"340", 340, true},
		{"2.5K", 2500, true},
		{"1B", 1000000000, true},
		{"", 0, false},
		{"followers", 0, false},
	}
	for _, c := range cases {
		got, ok := extract.ParseCompactInt(c.in)
		if ok != c.wantOk {
			t.Errorf("ParseCompactInt(%q) ok = %v, want %v", c.in, ok, c.wantOk)
			continue
		}
		if ok && got != c.want {
			t.Errorf("ParseCompactInt(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
