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
	statusOnly := flag.Bool("status-only", false, "read the current Full and Judge status without importing conclusions")
	approvedOnly := flag.Bool("approved-only", false, "carry only approved Full rows into a revised catalog; do not import Judge rows")
	approvedCaseHashes := flag.String("approved-case-hashes", "", "hash manifest proving approved cases are unchanged; required with approved-only")
	manifest := flag.String("manifest", catalogreview.DefaultManifestPath, "evaluation catalog manifest")
	governance := flag.String("governance", catalogreview.DefaultGovernancePath, "evaluation governance manifest")
	flag.Parse()
	if *statusOnly {
		if strings.TrimSpace(*reviewer) == "" || *validateOnly || *approvedOnly {
			fatal(errors.New("status-only requires reviewer and cannot be combined with import modes"))
		}
		catalog, judge := services(*manifest, *governance)
		full, err := catalog.List(context.Background(), *reviewer, catalogreview.Query{Status: "all", Page: 1, PageSize: 1})
		if err != nil {
			fatal(err)
		}
		judgeAudit, err := judge.Audit(context.Background(), *reviewer)
		if err != nil {
			fatal(err)
		}
		printJSON(struct {
			CatalogSHA256    string `json:"catalog_sha256"`
			GovernanceSHA256 string `json:"governance_sha256"`
			FullProgress     any    `json:"full_progress"`
			JudgePrompt      string `json:"judge_prompt"`
			JudgeReportSHA   string `json:"judge_report_sha256"`
			JudgeAgreement   any    `json:"judge_agreement"`
		}{full.CatalogSHA256, full.GovernanceSHA256, full.Progress, judgeAudit.JudgePrompt, judgeAudit.ReportSHA256, judgeAudit.Agreement})
		return
	}
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
		printJSON(conclusions.Summary())
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
	catalog, judge := services(*manifest, *governance)
	report, err := humanreviewimport.ImportWithOptions(context.Background(), *reviewer, *workbookSHA, conclusions, catalog, judge, humanreviewimport.ImportOptions{ApprovedOnly: *approvedOnly, ApprovedCases: approvedCommitments})
	if err != nil {
		fatal(err)
	}
	printJSON(report)
}

func services(manifest, governance string) (*catalogreview.Service, *judgecalibration.Service) {
	if err := mysql.InitMysql(); err != nil {
		fatal(err)
	}
	catalog := catalogreview.NewService(
		catalogreview.NewFileArtifactStoreWithGovernance(manifest, governance),
		catalogreview.NewGormRepository(mysql.DB), time.Now,
	)
	judge := judgecalibration.NewGovernedService(
		judgecalibration.NewFileArtifactStore(judgecalibration.DefaultDatasetPath, judgecalibration.DefaultReportPath),
		governance, manifest, judgecalibration.NewGormRepository(mysql.DB), time.Now,
	)
	return catalog, judge
}

func printJSON(value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(encoded))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "human review import failed:", err)
	os.Exit(1)
}
