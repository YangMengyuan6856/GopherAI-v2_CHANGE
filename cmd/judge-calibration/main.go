package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"GopherAI/config"
	"GopherAI/internal/evaluation"

	modelOpenAI "github.com/cloudwego/eino-ext/components/model/openai"
)

const (
	defaultDatasetPath = "evals/devsupport-judge-calibration-v1.jsonl"
	defaultReportPath  = "/root/GopherAI_Runtime/evaluation/judge-calibration-latest.json"
)

func main() {
	datasetPath := flag.String("dataset", defaultDatasetPath, "fixed judge calibration dataset")
	reportPath := flag.String("report", defaultReportPath, "immutable calibration report output")
	flag.Parse()
	if err := run(*datasetPath, *reportPath); err != nil {
		fmt.Fprintln(os.Stderr, "judge calibration failed:", err)
		os.Exit(1)
	}
}

func run(datasetPath, reportPath string) error {
	if strings.TrimSpace(datasetPath) != defaultDatasetPath || strings.TrimSpace(reportPath) != defaultReportPath {
		return errors.New("judge calibration paths must use the fixed production contract")
	}
	content, err := os.ReadFile(datasetPath)
	if err != nil {
		return err
	}
	cases, err := evaluation.LoadJudgeCalibrationCases(bytes.NewReader(content))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(content)
	configuration := config.GetConfig()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return errors.New("OPENAI_API_KEY is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	chatModel, err := modelOpenAI.NewChatModel(ctx, &modelOpenAI.ChatModelConfig{BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagChatModelName})
	if err != nil {
		return err
	}
	judge, err := evaluation.NewLLMJudge(chatModel, configuration.RagChatModelName, evaluation.JudgeDefaultTimeout)
	if err != nil {
		return err
	}
	report, err := evaluation.RunJudgeCalibration(ctx, cases, hex.EncodeToString(digest[:]), time.Now(), judge)
	if err != nil {
		return err
	}
	if err := evaluation.WriteJudgeCalibrationReport(reportPath, report); err != nil {
		return err
	}
	fmt.Printf("report=%s dataset=%s model=%s completed=%d failed=%d technical_gate=%t\n", report.ReportSHA256, report.DatasetSHA256, report.ModelVersion, report.CompletedCount, report.FailedCount, report.TechnicalGatePassed)
	if !report.TechnicalGatePassed {
		return errors.New("one or more judge cases failed")
	}
	return nil
}
