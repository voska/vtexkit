package cli

import "testing"

// Agents hallucinate identifiers, and these values are interpolated straight
// into request paths.
func TestValidateIDRejectsInjection(t *testing.T) {
	bad := []struct{ name, id string }{
		{"empty", ""},
		{"query fragment", "134?foo=bar"},
		{"url fragment", "134#x"},
		{"percent encoding", "134%2F"},
		{"path traversal", "../../etc/passwd"},
		{"backslash", `134\..\x`},
		{"newline", "134\nGET /admin"},
		{"null byte", "134\x00"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateID(tt.id); err == nil {
				t.Errorf("validateID(%q) accepted a dangerous identifier", tt.id)
			}
		})
	}
}

func TestValidateIDAcceptsRealIDs(t *testing.T) {
	// Real values from both stores.
	good := []string{"134", "62", "6180", "1234-01", "v12345", "zonasulzsa"}
	for _, id := range good {
		if err := validateID(id); err != nil {
			t.Errorf("validateID(%q) = %v, want nil", id, err)
		}
	}
}
