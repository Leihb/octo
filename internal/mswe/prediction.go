package mswe

import (
	"encoding/json"
	"fmt"
	"io"
)

// Prediction is one line of the predictions file the Multi-SWE-bench harness
// consumes: the instance identity plus the model-produced diff.
type Prediction struct {
	Org      string `json:"org"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	FixPatch string `json:"fix_patch"`
}

// WritePredictions emits one JSON object per line (JSONL), the format
// run_evaluation expects for patch_files.
func WritePredictions(w io.Writer, preds []Prediction) error {
	enc := json.NewEncoder(w)
	for _, p := range preds {
		if err := enc.Encode(p); err != nil {
			return fmt.Errorf("mswe: encode prediction %s/%s#%d: %w", p.Org, p.Repo, p.Number, err)
		}
	}
	return nil
}
