package main

import "testing"

func TestStringList_Repeatable(t *testing.T) {
	var s stringList
	for _, v := range []string{"/a", "/b", "/c"} {
		if err := s.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if len(s) != 3 || s[0] != "/a" || s[2] != "/c" {
		t.Errorf("stringList = %v, want [/a /b /c]", s)
	}
	if got := s.String(); got != "/a,/b,/c" {
		t.Errorf("String() = %q, want /a,/b,/c", got)
	}
}
