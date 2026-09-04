package rag

import (
	"GopherAI/internal/contract"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var evidenceMarker = regexp.MustCompile(`\[E([1-9][0-9]*)\]`)

var ErrCitationVerification = errors.New("citation verification failed")

type CitationBuilder struct{}

func NewCitationBuilder() *CitationBuilder { return &CitationBuilder{} }

func (*CitationBuilder) BuildAndVerify(tenantID string, answer string, references []string, evidence []contract.Evidence) (string, []contract.Citation, error) {
	tenantID = strings.TrimSpace(tenantID)
	answer = strings.TrimSpace(answer)
	if tenantID == "" || answer == "" || len(references) == 0 || len(evidence) == 0 {
		return "", nil, ErrCitationVerification
	}
	byReference := make(map[string]contract.Evidence, len(evidence))
	for index, item := range evidence {
		if item.TenantID != tenantID || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.SourceID) == "" ||
			strings.TrimSpace(item.SourceVersion) == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Content) == "" ||
			item.LineStart < 1 || item.LineEnd < item.LineStart {
			return "", nil, fmt.Errorf("%w: invalid or unauthorized evidence", ErrCitationVerification)
		}
		byReference[fmt.Sprintf("E%d", index+1)] = item
	}

	requested := make(map[string]struct{}, len(references))
	ordered := make([]string, 0, len(references))
	for _, reference := range references {
		reference = strings.ToUpper(strings.Trim(strings.TrimSpace(reference), "[]"))
		if _, exists := byReference[reference]; !exists {
			return "", nil, fmt.Errorf("%w: unknown evidence reference %s", ErrCitationVerification, reference)
		}
		if _, duplicate := requested[reference]; duplicate {
			continue
		}
		requested[reference] = struct{}{}
		ordered = append(ordered, reference)
	}
	if len(ordered) == 0 {
		return "", nil, ErrCitationVerification
	}

	markers := evidenceMarker.FindAllStringSubmatch(answer, -1)
	if len(markers) == 0 {
		return "", nil, fmt.Errorf("%w: answer has no inline evidence marker", ErrCitationVerification)
	}
	for _, marker := range markers {
		reference := "E" + marker[1]
		if _, exists := requested[reference]; !exists {
			return "", nil, fmt.Errorf("%w: inline marker %s was not declared", ErrCitationVerification, reference)
		}
	}
	for reference := range requested {
		if !strings.Contains(answer, "["+reference+"]") {
			return "", nil, fmt.Errorf("%w: declared reference %s is not used", ErrCitationVerification, reference)
		}
	}

	citations := make([]contract.Citation, 0, len(ordered))
	replacements := make(map[string]string, len(ordered))
	for index, reference := range ordered {
		item := byReference[reference]
		citationNumber := index + 1
		replacements[reference] = strconv.Itoa(citationNumber)
		citations = append(citations, contract.Citation{
			ID: fmt.Sprintf("C%d", citationNumber), EvidenceID: item.ID, Document: item.Title,
			Version: item.SourceVersion, Section: item.Section, LineStart: item.LineStart, LineEnd: item.LineEnd,
		})
	}
	verifiedAnswer := evidenceMarker.ReplaceAllStringFunc(answer, func(marker string) string {
		reference := strings.Trim(marker, "[]")
		if replacement, exists := replacements[reference]; exists {
			return "[" + replacement + "]"
		}
		return marker
	})
	return verifiedAnswer, citations, nil
}
