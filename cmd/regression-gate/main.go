package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"GopherAI/internal/evaluation"
)

func main() {
	baselinePath := flag.String("baseline", "", "frozen baseline snapshot JSON")
	candidatePath := flag.String("candidate", "", "candidate snapshot JSON")
	outPath := flag.String("out", "", "optional regression report output path")
	flag.Parse()
	if *baselinePath == "" || *candidatePath == "" {
		fatal(errors.New("both -baseline and -candidate are required"))
	}

	baseline, err := loadSnapshot(*baselinePath)
	if err != nil {
		fatal(err)
	}
	candidate, err := loadSnapshot(*candidatePath)
	if err != nil {
		fatal(err)
	}
	report, err := evaluation.EvaluateRegression(baseline, candidate, time.Now())
	if err != nil {
		fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	encoded = append(encoded, '\n')
	if *outPath == "" {
		_, _ = os.Stdout.Write(encoded)
	} else if err := os.WriteFile(*outPath, encoded, 0o644); err != nil {
		fatal(err)
	}
	if report.Blocked {
		os.Exit(2)
	}
}

func loadSnapshot(path string) (evaluation.EvaluationSnapshot, error) {
	var snapshot evaluation.EvaluationSnapshot
	file, err := os.Open(path)
	if err != nil {
		return snapshot, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return snapshot, fmt.Errorf("decode %s: trailing content", path)
	}
	return snapshot, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
