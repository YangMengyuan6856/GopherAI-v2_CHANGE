package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"GopherAI/common/mysql"
	"GopherAI/internal/catalogreview"
	"GopherAI/internal/humanreviewimport"
	"GopherAI/internal/judgecalibration"
)

func main() {
	input := flag.String("input", "", "UTF-8 350-row conclusions TXT")
	reviewer := flag.String("reviewer", "", "existing GopherAI username that owns the human review")
	workbookSHA := flag.String("workbook-sha256", "", "optional SHA-256 of the filled DOCX")
	validateOnly := flag.Bool("validate-only", false, "validate and summarize the conclusions without writing MySQL")
	approvedOnly := flag.Bool("approved-only", false, "carry only approved Full rows into a revised catalog; do not import Judge rows")
	approvedCaseHashes := flag.String("approved-case-hashes", "", "hash manifest proving approved cases are unchanged; required with approved-only")
	manifest := flag.String("manifest", catalogreview.DefaultManifestPath, "evaluation catalog manifest")
	governance := flag.String("governance", catalogreview.DefaultGovernancePath, "evaluation governance manifest")
	flag.Parse()
	if strings.TrimSpace(*input) == "" || (!*validateOnly && strings.TrimSpace(*reviewer) == "") {
		fatal(errors.New("input is required; reviewer is also required for import"))
	}
	file, err := os.Open(*input)
	if err != nil {
		fatal(err)
	}
	conclusions, err := humanreviewimport.Parse(file)
	_ = file.Close()
	if err != nil {
		fatal(err)
	}
	if *validateOnly {
		encoded, marshalErr := json.MarshalIndent(conclusions.Summary(), "", "  ")
		if marshalErr != nil {
			fatal(marshalErr)
		}
		fmt.Println(string(encoded))
		return
	}
	approvedCommitments := map[int]humanreviewimport.ApprovedCaseCommitment{}
	if *approvedOnly {
		if strings.TrimSpace(*approvedCaseHashes) == "" {
			fatal(errors.New("approved-case-hashes is required with approved-only"))
		}
		commitmentFile, openErr := os.Open(*approvedCaseHashes)
		if openErr != nil {
			fatal(openErr)
		}
		approvedManifest, loadErr := humanreviewimport.LoadApprovedCaseManifest(commitmentFile, conclusions.SourceSHA256)
		_ = commitmentFile.Close()
		if loadErr != nil {
			fatal(loadErr)
		}
		approvedCommitments = approvedManifest.ByOrdinal()
	}
	if err := mysql.InitMysql(); err != nil {
		fatal(err)
	}
	catalog := catalogreview.NewService(
		catalogreview.NewFileArtifactStoreWithGovernance(*manifest, *governance),
		catalogreview.NewGormRepository(mysql.DB), time.Now,
	)
	judge := judgecalibration.NewGovernedService(
		judgecalibration.NewFileArtifactStore(judgecalibration.DefaultDatasetPath, judgecalibration.DefaultReportPath),
		*governance, *manifest, judgecalibration.NewGormRepository(mysql.DB), time.Now,
	)
	report, err := humanreviewimport.ImportWithOptions(context.Background(), *reviewer, *workbookSHA, conclusions, catalog, judge, humanreviewimport.ImportOptions{ApprovedOnly: *approvedOnly, ApprovedCases: approvedCommitments})
	if err != nil {
		fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(encoded))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "human review import failed:", err)
	os.Exit(1)
}
