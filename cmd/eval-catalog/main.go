package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"GopherAI/internal/evaluation"
)

func main() {
	manifestPath := flag.String("manifest", "evals/devsupport-eval-v1.manifest.json", "evaluation catalog manifest")
	outPath := flag.String("out", "", "optional validation report path")
	flag.Parse()
	report, err := evaluation.ValidateEvalCatalogFile(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')
	if *outPath != "" {
		if err := os.WriteFile(*outPath, encoded, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		_, _ = os.Stdout.Write(encoded)
	}
	if !report.Passed {
		os.Exit(2)
	}
}
