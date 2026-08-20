package cli

import "testing"

func TestShortDateTrimsToTheDay(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// What RNS actually sends.
		{"rfc3339", "2026-09-03T09:00:00Z", "2026-09-03"},
		{"with offset", "2026-09-03T09:00:00-03:00", "2026-09-03"},
		// A subscription that has never shipped has no last purchase date.
		{"empty", "", "—"},
		// Passing an unparseable value through beats rendering a
		// confident-looking zero date the store never sent.
		{"unparseable", "not a date", "not a date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortDate(tt.in); got != tt.want {
				t.Errorf("shortDate(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSkipNoteOnlyMarksSkipped(t *testing.T) {
	if skipNote(false) != "" {
		t.Error("an unskipped subscription must render no note")
	}
	if skipNote(true) == "" {
		t.Error("a skipped subscription must be visibly marked")
	}
}
