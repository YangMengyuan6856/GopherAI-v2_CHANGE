package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"GopherAI/internal/catalogrerun"
)

func main() {
	artifact := flag.String("artifact", "", "path to a validated immutable Full 320 sealed candidate")
	outputRoot := flag.String("output-root", catalogrerun.DefaultOutputRoot, "create-only technical rerun evidence root")
	workingDirectory := flag.String("working-directory", ".", "release directory containing the fixed evaluation binaries")
	releaseManifest := flag.String("release-manifest", "release-manifest.json", "current immutable release manifest")
	execute := flag.Bool("execute", false, "execute the fixed six-step rerun; omission prints a read-only plan")
	acknowledgment := flag.String("acknowledgment", "", "required exact execution acknowledgment")
	verifyRun := flag.String("verify-run", "", "independently validate an existing rerun evidence directory")
	flag.Parse()
	if strings.TrimSpace(*verifyRun) != "" {
		if *execute || strings.TrimSpace(*artifact) != "" || strings.TrimSpace(*acknowledgment) != "" {
			fatal(errors.New("verify-run cannot be combined with plan or execution flags"), 1)
		}
		report, err := catalogrerun.ValidateRunDirectory(*verifyRun)
		if err != nil {
			fatal(err, 1)
		}
		writeJSON(report)
		return
	}
	if strings.TrimSpace(*artifact) == "" {
		fatal(errors.New("artifact is required"), 1)
	}
	plan, err := catalogrerun.BuildPlan(*artifact, *outputRoot, *workingDirectory, *releaseManifest)
	if err != nil {
		fatal(err, 1)
	}
	if !*execute {
		writeJSON(plan)
		return
	}
	if *acknowledgment != catalogrerun.Acknowledgment {
		fatal(errors.New("exact execution acknowledgment is required"), 1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Minute)
	defer cancel()
	report, runDirectory, err := catalogrerun.Execute(ctx, plan, catalogrerun.OSExecutor{}, time.Now)
	if err != nil {
		if runDirectory != "" {
			fmt.Fprintln(os.Stderr, "sealed catalog rerun evidence:", filepath.Clean(runDirectory))
		}
		fatal(err, 1)
	}
	writeJSON(report)
	if report.Status == "completed_technical_gate_failed" {
		os.Exit(2)
	}
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		fatal(err, 1)
	}
}

func fatal(err error, code int) {
	fmt.Fprintln(os.Stderr, "sealed catalog rerun failed:", err)
	os.Exit(code)
}
