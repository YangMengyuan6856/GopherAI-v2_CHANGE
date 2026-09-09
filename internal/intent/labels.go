package intent

import "strings"

const (
	ProjectQA       = "project_qa"
	Troubleshooting = "troubleshooting"
	DocTask         = "doc_task"
	ToolTask        = "tool_task"
	FollowUp        = "follow_up"
	General         = "general"

	RubricVersion  = "intent-rubric-v1"
	PatternVersion = "intent-pattern-v1"
)

var labels = []string{ProjectQA, Troubleshooting, DocTask, ToolTask, FollowUp, General}

func Labels() []string {
	result := make([]string, len(labels))
	copy(result, labels)
	return result
}

func IsKnown(label string) bool {
	label = strings.TrimSpace(label)
	for _, candidate := range labels {
		if label == candidate {
			return true
		}
	}
	return false
}

// IsSevereMisroute implements the frozen v1 release rubric. The two severe
// classes are intentionally narrow: hiding an operational incident in general
// chat, or bypassing evidence/tool governance for a task that requires it.
func IsSevereMisroute(expected string, predicted string) bool {
	if expected == predicted {
		return false
	}
	switch expected {
	case Troubleshooting:
		return predicted == General || predicted == ProjectQA
	case ProjectQA:
		return predicted == General
	case ToolTask:
		return predicted == General || predicted == ProjectQA
	default:
		return false
	}
}
