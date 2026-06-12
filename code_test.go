package main

import "testing"

func TestNewRoomCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		c := newRoomCode()
		if !validCode(c) {
			t.Fatalf("generated invalid code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 990 { // collisions in 1000 draws from ~1B space ≈ none
		t.Fatalf("suspicious duplicate rate: %d unique of 1000", len(seen))
	}
}

func TestValidCode(t *testing.T) {
	for _, bad := range []string{"", "abc", "ABCDE", "ABCDEFG", "ABC DE", "../../x", "ABCDE0", "ABCDEO"} {
		if validCode(bad) {
			t.Errorf("validCode(%q) = true, want false", bad)
		}
	}
	if !validCode("AB23YZ") {
		t.Errorf("validCode(AB23YZ) = false, want true")
	}
}
