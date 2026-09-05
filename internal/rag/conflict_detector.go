package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"GopherAI/internal/contract"
)

const (
	EvidenceConflictStatusReview = "requires_user_confirmation"
	EvidenceConflictVersion      = "evidence-conflict-v1"
)

var scalarFactPattern = regexp.MustCompile(`(?i)^\s*["']?([a-z_][a-z0-9_.-]*)["']?\s*[:=]\s*(.+?)\s*[,;]?\s*$`)

var ignoredConflictKeys = map[string]struct{}{
	"id": {}, "name": {}, "type": {}, "status": {}, "description": {}, "enabled": {}, "version": {},
}

type factObservation struct {
	key        string
	normalized string
	value      contract.EvidenceConflictValue
}

func DetectEvidenceConflicts(hits []SearchHit) []contract.EvidenceConflict {
	byFact := make(map[string][]factObservation)
	for _, hit := range hits {
		for _, observation := range extractFactObservations(hit.Evidence) {
			byFact[observation.key] = append(byFact[observation.key], observation)
		}
	}
	keys := make([]string, 0, len(byFact))
	for key := range byFact {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	conflicts := make([]contract.EvidenceConflict, 0)
	for _, key := range keys {
		observations := byFact[key]
		values := make(map[string]contract.EvidenceConflictValue)
		sources := make(map[string]struct{})
		for _, observation := range observations {
			sourceIdentity := observation.value.SourceID + "\x00" + observation.value.SourceRevision + "\x00" + observation.value.SourceVersion
			sources[sourceIdentity] = struct{}{}
			if _, exists := values[observation.normalized]; !exists {
				values[observation.normalized] = observation.value
			}
		}
		if len(values) < 2 || len(sources) < 2 {
			continue
		}
		normalizedValues := make([]string, 0, len(values))
		for normalized := range values {
			normalizedValues = append(normalizedValues, normalized)
		}
		sort.Strings(normalizedValues)
		conflictValues := make([]contract.EvidenceConflictValue, 0, len(normalizedValues))
		for _, normalized := range normalizedValues {
			conflictValues = append(conflictValues, values[normalized])
		}
		digest := sha256.Sum256([]byte(key + "\x00" + strings.Join(normalizedValues, "\x00")))
		conflicts = append(conflicts, contract.EvidenceConflict{
			ConflictID: "conflict-" + hex.EncodeToString(digest[:8]), FactKey: key,
			Status: EvidenceConflictStatusReview, Values: conflictValues,
		})
	}
	return conflicts
}

func extractFactObservations(evidence contract.Evidence) []factObservation {
	result := make([]factObservation, 0, 4)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(evidence.Content, "\n") {
		match := scalarFactPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[1]))
		if _, ignored := ignoredConflictKeys[key]; ignored {
			continue
		}
		value := cleanScalarValue(match[2])
		if value == "" || utf8.RuneCountInString(value) > 128 || strings.ContainsAny(value, "{}[]") {
			continue
		}
		factKey := canonicalFactKey(evidence.Section, key)
		normalizedValue := strings.ToLower(strings.Join(strings.Fields(value), " "))
		dedupeKey := factKey + "\x00" + normalizedValue
		if _, exists := seen[dedupeKey]; exists {
			continue
		}
		seen[dedupeKey] = struct{}{}
		result = append(result, factObservation{
			key: factKey, normalized: normalizedValue,
			value: contract.EvidenceConflictValue{
				Value: value, EvidenceID: evidence.ID, SourceID: evidence.SourceID, SourceTitle: evidence.Title, SourceVersion: evidence.SourceVersion,
				SourceRevision: evidence.SourceRevision, Authority: evidence.Authority, EffectiveAt: evidence.EffectiveAt,
			},
		})
	}
	return result
}

func cleanScalarValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ",")
	value = strings.TrimSuffix(value, ";")
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func canonicalFactKey(section string, key string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(section)), " > ")
	cleaned := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		if part = strings.Join(strings.Fields(part), " "); part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return key
	}
	if cleaned[len(cleaned)-1] != key {
		cleaned = append(cleaned, key)
	}
	return strings.Join(cleaned, " > ")
}
