package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"GopherAI/internal/cleanupaudit"
	"GopherAI/internal/observability"
)

const defaultReportPath = "/root/GopherAI_Runtime/evaluation/cleanup-audit-latest.json"

type commandOptions struct {
	releaseID  string
	gitSHA     string
	root       string
	reportPath string
	timeout    time.Duration
}

type commandResult struct {
	SchemaVersion      string  `json:"schema_version"`
	ReleaseID          string  `json:"release_id"`
	GitSHA             string  `json:"git_sha"`
	ReportSHA256       string  `json:"report_sha256"`
	CleanupComplete    bool    `json:"cleanup_complete"`
	AlreadyRemoved     int     `json:"already_removed"`
	RetainedRequired   int     `json:"retained_required"`
	EligibleToDelete   int     `json:"eligible_to_delete"`
	Blocked            int     `json:"blocked"`
	RetiredEntryCalls  float64 `json:"retired_entry_calls_24h"`
	TrackedSourceCount int     `json:"tracked_source_count"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	options, err := parseOptions(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup audit arguments are invalid: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()
	builder := cleanupaudit.NewBuilder(options.root, observability.NewDefaultPrometheusRuntimeClient(), time.Now)
	report, err := builder.Build(ctx, options.releaseID, options.gitSHA)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup audit failed: %v\n", err)
		return 1
	}
	if !report.Summary.CleanupComplete || report.Summary.EligibleToDelete != 0 || report.Summary.Blocked != 0 {
		fmt.Fprintf(stderr, "cleanup audit gate rejected release: complete=%t eligible=%d blocked=%d\n", report.Summary.CleanupComplete, report.Summary.EligibleToDelete, report.Summary.Blocked)
		return 1
	}
	if err := cleanupaudit.NewFileStore(options.reportPath).Save(report); err != nil {
		fmt.Fprintf(stderr, "cleanup audit persistence failed: %v\n", err)
		return 1
	}
	result := commandResult{
		SchemaVersion: cleanupaudit.SchemaVersion, ReleaseID: report.ReleaseID, GitSHA: report.GitSHA,
		ReportSHA256: report.ReportSHA256, CleanupComplete: report.Summary.CleanupComplete,
		AlreadyRemoved: report.Summary.AlreadyRemoved, RetainedRequired: report.Summary.RetainedRequired,
		EligibleToDelete: report.Summary.EligibleToDelete, Blocked: report.Summary.Blocked,
		RetiredEntryCalls: report.Observation.AttemptCount, TrackedSourceCount: report.TrackedSourceCount,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "cleanup audit output failed: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (commandOptions, error) {
	options := commandOptions{}
	flags := flag.NewFlagSet("cleanup-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.releaseID, "release-id", "", "current immutable release id")
	flags.StringVar(&options.gitSHA, "git-sha", "", "current 40 character Git SHA")
	flags.StringVar(&options.root, "root", ".", "packaged source root")
	flags.StringVar(&options.reportPath, "report", defaultReportPath, "atomic report destination")
	flags.DurationVar(&options.timeout, "timeout", 12*time.Second, "bounded Prometheus and source audit timeout")
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 0 {
		return commandOptions{}, errors.New("positional arguments are not supported")
	}
	options.releaseID = strings.TrimSpace(options.releaseID)
	options.gitSHA = strings.ToLower(strings.TrimSpace(options.gitSHA))
	options.root = strings.TrimSpace(options.root)
	options.reportPath = strings.TrimSpace(options.reportPath)
	if options.releaseID == "" || strings.ContainsAny(options.releaseID, " \t\r\n/\\") {
		return commandOptions{}, errors.New("release-id is required and must be path-safe")
	}
	decoded, err := hex.DecodeString(options.gitSHA)
	if err != nil || len(decoded) != 20 {
		return commandOptions{}, errors.New("git-sha must be a full 40 character hexadecimal SHA")
	}
	if options.root == "" || options.reportPath == "" || options.timeout <= 0 || options.timeout > time.Minute {
		return commandOptions{}, errors.New("root, report and timeout must use bounded non-empty values")
	}
	return options, nil
}
