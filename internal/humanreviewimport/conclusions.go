package humanreviewimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"GopherAI/internal/evaluation"
)

const (
	FullCaseCount  = 320
	JudgeCaseCount = 30
	TotalLineCount = FullCaseCount + JudgeCaseCount
)

var (
	ErrInvalidConclusions = errors.New("human review conclusions are invalid")
	allowedRejectReasons  = map[string]struct{}{
		"ambiguous_input": {}, "expected_result_incorrect": {}, "missing_context": {},
		"schema_issue": {}, "unsafe_or_sensitive": {}, "provenance_missing": {},
		"criterion_not_independent": {}, "scenario_not_representative": {}, "not_discriminative": {},
	}
	judgeFields = []string{"相关性", "完整性", "有用性", "有依据", "安全性"}
)

type FullConclusion struct {
	Ordinal     int      `json:"ordinal"`
	Decision    string   `json:"decision"`
	ReasonCodes []string `json:"reason_codes"`
	Comment     string   `json:"comment"`
}

type JudgeConclusion struct {
	Ordinal int                    `json:"ordinal"`
	Scores  evaluation.JudgeScores `json:"scores"`
	Comment string                 `json:"comment"`
}

type Conclusions struct {
	SourceSHA256 string            `json:"source_sha256"`
	Full         []FullConclusion  `json:"full"`
	Judge        []JudgeConclusion `json:"judge"`
}

type Summary struct {
	SourceSHA256 string         `json:"source_sha256"`
	FullCount    int            `json:"full_count"`
	Approved     int            `json:"approved"`
	Rejected     int            `json:"rejected"`
	JudgeCount   int            `json:"judge_count"`
	ReasonCounts map[string]int `json:"reason_counts"`
}

func Parse(reader io.Reader) (Conclusions, error) {
	if reader == nil {
		return Conclusions{}, ErrInvalidConclusions
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil || len(encoded) == 0 || len(encoded) >= 1<<20 || !utf8.Valid(encoded) {
		return Conclusions{}, ErrInvalidConclusions
	}
	digest := sha256.Sum256(encoded)
	result := Conclusions{SourceSHA256: hex.EncodeToString(digest[:]), Full: make([]FullConclusion, 0, FullCaseCount), Judge: make([]JudgeConclusion, 0, JudgeCaseCount)}
	scanner := bufio.NewScanner(bytes.NewReader(encoded))
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	lineNumber := 0
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" {
			continue
		}
		lineNumber++
		if lineNumber <= FullCaseCount {
			item, parseErr := parseFullLine(line, lineNumber)
			if parseErr != nil {
				return Conclusions{}, parseErr
			}
			result.Full = append(result.Full, item)
			continue
		}
		item, parseErr := parseJudgeLine(line, lineNumber-FullCaseCount)
		if parseErr != nil {
			return Conclusions{}, parseErr
		}
		result.Judge = append(result.Judge, item)
	}
	if err := scanner.Err(); err != nil {
		return Conclusions{}, fmt.Errorf("%w: read source", ErrInvalidConclusions)
	}
	if lineNumber != TotalLineCount || len(result.Full) != FullCaseCount || len(result.Judge) != JudgeCaseCount {
		return Conclusions{}, fmt.Errorf("%w: require 320 Full and 30 Judge rows, got %d and %d", ErrInvalidConclusions, len(result.Full), len(result.Judge))
	}
	return result, nil
}

func (conclusions Conclusions) Summary() Summary {
	summary := Summary{SourceSHA256: conclusions.SourceSHA256, FullCount: len(conclusions.Full), JudgeCount: len(conclusions.Judge), ReasonCounts: map[string]int{}}
	for _, item := range conclusions.Full {
		if item.Decision == "approved" {
			summary.Approved++
			continue
		}
		summary.Rejected++
		for _, reason := range item.ReasonCodes {
			summary.ReasonCounts[reason]++
		}
	}
	return summary
}

func parseFullLine(line string, ordinal int) (FullConclusion, error) {
	parts := strings.SplitN(line, "|", 4)
	if len(parts) < 3 || parts[0] != fmt.Sprintf("%03d", ordinal) {
		return FullConclusion{}, fmt.Errorf("%w: Full row %03d identity", ErrInvalidConclusions, ordinal)
	}
	decision := strings.TrimSpace(parts[1])
	item := FullConclusion{Ordinal: ordinal}
	switch decision {
	case "APPROVED":
		if len(parts) != 3 {
			return FullConclusion{}, fmt.Errorf("%w: approved Full row %03d shape", ErrInvalidConclusions, ordinal)
		}
		item.Decision, item.ReasonCodes, item.Comment = "approved", []string{"label_verified"}, strings.TrimSpace(parts[2])
	case "REJECTED":
		if len(parts) != 4 || !strings.HasPrefix(strings.TrimSpace(parts[2]), "原因码：") {
			return FullConclusion{}, fmt.Errorf("%w: rejected Full row %03d shape", ErrInvalidConclusions, ordinal)
		}
		item.Decision, item.Comment = "rejected", strings.TrimSpace(parts[3])
		for _, reason := range strings.Split(strings.TrimPrefix(strings.TrimSpace(parts[2]), "原因码："), ",") {
			reason = strings.TrimSpace(reason)
			if _, allowed := allowedRejectReasons[reason]; !allowed {
				return FullConclusion{}, fmt.Errorf("%w: Full row %03d reason %q", ErrInvalidConclusions, ordinal, reason)
			}
			item.ReasonCodes = append(item.ReasonCodes, reason)
		}
		sort.Strings(item.ReasonCodes)
		if len(item.ReasonCodes) < 1 || len(item.ReasonCodes) > 3 || hasDuplicate(item.ReasonCodes) {
			return FullConclusion{}, fmt.Errorf("%w: Full row %03d reason count", ErrInvalidConclusions, ordinal)
		}
	default:
		return FullConclusion{}, fmt.Errorf("%w: Full row %03d decision", ErrInvalidConclusions, ordinal)
	}
	if item.Comment == "" || len([]rune(item.Comment)) > 4000 {
		return FullConclusion{}, fmt.Errorf("%w: Full row %03d comment", ErrInvalidConclusions, ordinal)
	}
	return item, nil
}

func parseJudgeLine(line string, ordinal int) (JudgeConclusion, error) {
	parts := strings.SplitN(line, "|", 7)
	if len(parts) != 7 || parts[0] != fmt.Sprintf("Judge%03d", ordinal) {
		return JudgeConclusion{}, fmt.Errorf("%w: Judge row %03d shape", ErrInvalidConclusions, ordinal)
	}
	values := make([]float64, 5)
	for index, field := range judgeFields {
		prefix := field + "="
		if !strings.HasPrefix(parts[index+1], prefix) {
			return JudgeConclusion{}, fmt.Errorf("%w: Judge row %03d field %s", ErrInvalidConclusions, ordinal, field)
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(parts[index+1], prefix)), 64)
		if err != nil || value < 0 || value > 1 || value*4 != float64(int(value*4)) {
			return JudgeConclusion{}, fmt.Errorf("%w: Judge row %03d score %s", ErrInvalidConclusions, ordinal, field)
		}
		values[index] = value
	}
	comment := strings.TrimSpace(parts[6])
	if comment == "" || len([]rune(comment)) > 4000 {
		return JudgeConclusion{}, fmt.Errorf("%w: Judge row %03d comment", ErrInvalidConclusions, ordinal)
	}
	return JudgeConclusion{Ordinal: ordinal, Scores: evaluation.JudgeScores{
		Relevance: values[0], Completeness: values[1], Helpfulness: values[2], Groundedness: values[3], Safety: values[4],
	}, Comment: comment}, nil
}

func hasDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}
