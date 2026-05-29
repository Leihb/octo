package mswe

import (
	"encoding/json"
	"fmt"
	"io"
)

// HarnessConfig is the config.json passed to
// `python -m multi_swe_bench.harness.run_evaluation --config <file>`. It points
// the judge at the dataset records and our predictions, and at scratch/output
// dirs for the per-instance Docker builds and the final report.
//
// Field names mirror the harness's documented config keys. The harness may
// accept or require additional keys depending on its release; this covers the
// documented set and leaves the rest at the harness defaults. Confirm against
// the installed multi_swe_bench version on the first run.
type HarnessConfig struct {
	Mode                  string   `json:"mode"`
	Workdir               string   `json:"workdir"`
	PatchFiles            []string `json:"patch_files"`
	DatasetFiles          []string `json:"dataset_files"`
	OutputDir             string   `json:"output_dir"`
	ForceBuild            bool     `json:"force_build"`
	MaxWorkersBuildImage  int      `json:"max_workers_build_image"`
	MaxWorkersRunInstance int      `json:"max_workers_run_instance"`
}

// NewHarnessConfig builds a config with conservative defaults: serial Docker
// builds and runs (workers=1) so a small first run is easy to follow and
// doesn't thrash the machine.
func NewHarnessConfig(workdir, datasetFile, predictionsFile, outputDir string) HarnessConfig {
	return HarnessConfig{
		Mode:                  "evaluation",
		Workdir:               workdir,
		PatchFiles:            []string{predictionsFile},
		DatasetFiles:          []string{datasetFile},
		OutputDir:             outputDir,
		ForceBuild:            false,
		MaxWorkersBuildImage:  1,
		MaxWorkersRunInstance: 1,
	}
}

// Write emits the config as indented JSON.
func (c HarnessConfig) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("mswe: write harness config: %w", err)
	}
	return nil
}
