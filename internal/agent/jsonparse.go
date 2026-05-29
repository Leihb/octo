package agent

import "strings"

// stripCodeFence removes a leading ```… fence and trailing ``` if present, so a
// fenced JSON block parses.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:] // drop the ```lang line
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// firstJSONChar returns the first '{' or '[' byte in s, scanning past
// surrounding prose. Used to choose between object/array parse paths so an
// array of objects isn't misread as a bare object.
func firstJSONChar(s string) (byte, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			return s[i], true
		}
	}
	return 0, false
}

// sliceBetween returns the substring from the first open rune to the last
// close rune (inclusive), or "" if either is missing. Used to forgive a model
// that wraps the JSON in surrounding prose.
func sliceBetween(s string, open, close byte) string {
	i := strings.IndexByte(s, open)
	j := strings.LastIndexByte(s, close)
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return s[i : j+1]
}
