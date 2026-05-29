package mswe

import (
	"encoding/json"
	"fmt"
)

// Summary is the headline result parsed from the harness's final_report.json.
type Summary struct {
	Resolved   int
	Unresolved int
	Total      int
}

// ParseReport reads final_report.json and extracts resolved/unresolved counts.
// It tolerates the two shapes the harness might use for each field — a list of
// instance IDs or a bare count — so it survives minor format differences.
func ParseReport(data []byte) (Summary, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Summary{}, fmt.Errorf("mswe: parse report: %w", err)
	}
	res := countField(raw, "resolved_instances", "resolved")
	unres := countField(raw, "unresolved_instances", "unresolved")
	total := countField(raw, "total_instances", "total")
	if total == 0 {
		total = res + unres
	}
	return Summary{Resolved: res, Unresolved: unres, Total: total}, nil
}

// countField returns the count for the first present key, treating a JSON array
// as its length and a JSON number as its value.
func countField(raw map[string]json.RawMessage, keys ...string) int {
	for _, k := range keys {
		v, ok := raw[k]
		if !ok {
			continue
		}
		var list []json.RawMessage
		if err := json.Unmarshal(v, &list); err == nil {
			return len(list)
		}
		var n int
		if err := json.Unmarshal(v, &n); err == nil {
			return n
		}
	}
	return 0
}
