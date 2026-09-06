package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	ControlAcceptanceSchemaVersion = "harness-control-acceptance-v1"
	ControlTransitionVersion       = "harness-pointer-cas-v1"
	ControlAcceptanceMode          = "deterministic_in_memory_no_write"
)

var (
	ErrHumanApprovalRequired = errors.New("human approval is required")
	ErrOfflineGateRequired   = errors.New("offline promotion gate is required")
	ErrShadowGateRequired    = errors.New("isolated shadow must pass")
	ErrSafetyGateRequired    = errors.New("safety gate must pass")
	ErrParentVersionMismatch = errors.New("candidate parent version does not match active pointer")
	ErrPointerStateConflict  = errors.New("active pointer state version conflict")
	ErrRollbackUnavailable   = errors.New("no rollback target is available")
)

type ControlCandidate struct {
	ArtifactType    string `json:"artifact_type"`
	ArtifactVersion string `json:"artifact_version"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	ParentVersion   string `json:"parent_version"`
	ParentSHA256    string `json:"parent_sha256"`
}

type ControlAdmission struct {
	OfflineGatePassed bool `json:"offline_gate_passed"`
	HumanApproved     bool `json:"human_approved"`
	ShadowPassed      bool `json:"shadow_passed"`
	SafetyPassed      bool `json:"safety_passed"`
}

type ActiveHarnessPointer struct {
	ArtifactType    string `json:"artifact_type"`
	CurrentVersion  string `json:"current_version"`
	CurrentSHA256   string `json:"current_sha256"`
	PreviousVersion string `json:"previous_version,omitempty"`
	PreviousSHA256  string `json:"previous_sha256,omitempty"`
	StateVersion    uint64 `json:"state_version"`
	LastTransition  string `json:"last_transition"`
}

type ControlAcceptanceCase struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	ReasonCode string `json:"reason_code"`
}

type ControlAcceptanceReport struct {
	SchemaVersion            string                  `json:"schema_version"`
	TransitionVersion        string                  `json:"transition_version"`
	Mode                     string                  `json:"mode"`
	GeneratedAt              time.Time               `json:"generated_at"`
	CaseCount                int                     `json:"case_count"`
	PassedCount              int                     `json:"passed_count"`
	ProductionWrites         int                     `json:"production_writes"`
	ProductionActivePointers int                     `json:"production_active_pointers"`
	Cases                    []ControlAcceptanceCase `json:"cases"`
	Guardrails               []string                `json:"guardrails"`
	Limitations              []string                `json:"limitations"`
	ReportSHA256             string                  `json:"report_sha256"`
}

func ActivateHarnessPointer(current ActiveHarnessPointer, candidate ControlCandidate, admission ControlAdmission, expectedStateVersion uint64) (ActiveHarnessPointer, error) {
	if !admission.OfflineGatePassed {
		return ActiveHarnessPointer{}, ErrOfflineGateRequired
	}
	if !admission.HumanApproved {
		return ActiveHarnessPointer{}, ErrHumanApprovalRequired
	}
	if !admission.ShadowPassed {
		return ActiveHarnessPointer{}, ErrShadowGateRequired
	}
	if !admission.SafetyPassed {
		return ActiveHarnessPointer{}, ErrSafetyGateRequired
	}
	if current.StateVersion != expectedStateVersion {
		return ActiveHarnessPointer{}, ErrPointerStateConflict
	}
	if !validControlActivationParent(current) || !validControlCandidate(candidate) || current.ArtifactType != candidate.ArtifactType ||
		current.CurrentVersion != candidate.ParentVersion || current.CurrentSHA256 != candidate.ParentSHA256 {
		return ActiveHarnessPointer{}, ErrParentVersionMismatch
	}
	return ActiveHarnessPointer{
		ArtifactType: candidate.ArtifactType, CurrentVersion: candidate.ArtifactVersion, CurrentSHA256: candidate.ArtifactSHA256,
		PreviousVersion: current.CurrentVersion, PreviousSHA256: current.CurrentSHA256, StateVersion: current.StateVersion + 1,
		LastTransition: "activated_after_human_approval_and_shadow",
	}, nil
}

func RollbackHarnessPointer(current ActiveHarnessPointer, expectedStateVersion uint64) (ActiveHarnessPointer, error) {
	if !validControlPointer(current) {
		return ActiveHarnessPointer{}, ErrRollbackUnavailable
	}
	if current.StateVersion != expectedStateVersion {
		return ActiveHarnessPointer{}, ErrPointerStateConflict
	}
	if current.PreviousVersion == "" || len(current.PreviousSHA256) != 64 {
		return ActiveHarnessPointer{}, ErrRollbackUnavailable
	}
	return ActiveHarnessPointer{
		ArtifactType: current.ArtifactType, CurrentVersion: current.PreviousVersion, CurrentSHA256: current.PreviousSHA256,
		StateVersion: current.StateVersion + 1, LastTransition: "rolled_back_to_previous_version",
	}, nil
}

func RunControlAcceptance(ctx context.Context, generatedAt time.Time) (ControlAcceptanceReport, error) {
	if err := ctx.Err(); err != nil {
		return ControlAcceptanceReport{}, err
	}
	baseline := controlBaselinePointer()
	candidate := controlCandidate("prompt-template-candidate-v2", digestString("candidate-v2"))
	otherCandidate := controlCandidate("prompt-template-candidate-v3", digestString("candidate-v3"))
	fullAdmission := ControlAdmission{OfflineGatePassed: true, HumanApproved: true, ShadowPassed: true, SafetyPassed: true}
	cases := make([]ControlAcceptanceCase, 0, 10)
	appendCase := func(name, reason string, passed bool) {
		cases = append(cases, ControlAcceptanceCase{Name: name, ReasonCode: reason, Passed: passed})
	}

	_, err := ActivateHarnessPointer(baseline, candidate, ControlAdmission{OfflineGatePassed: true, ShadowPassed: true, SafetyPassed: true}, baseline.StateVersion)
	appendCase("no human approval cannot activate", "human_approval_required", errors.Is(err, ErrHumanApprovalRequired))
	_, err = ActivateHarnessPointer(baseline, candidate, ControlAdmission{HumanApproved: true, ShadowPassed: true, SafetyPassed: true}, baseline.StateVersion)
	appendCase("offline gate is mandatory", "offline_gate_required", errors.Is(err, ErrOfflineGateRequired))
	_, err = ActivateHarnessPointer(baseline, candidate, ControlAdmission{OfflineGatePassed: true, HumanApproved: true, SafetyPassed: true}, baseline.StateVersion)
	appendCase("failed shadow cannot activate", "shadow_gate_required", errors.Is(err, ErrShadowGateRequired))
	_, err = ActivateHarnessPointer(baseline, candidate, ControlAdmission{OfflineGatePassed: true, HumanApproved: true, ShadowPassed: true}, baseline.StateVersion)
	appendCase("safety regression cannot activate", "safety_gate_required", errors.Is(err, ErrSafetyGateRequired))
	wrongParent := candidate
	wrongParent.ParentVersion = "unknown-parent"
	_, err = ActivateHarnessPointer(baseline, wrongParent, fullAdmission, baseline.StateVersion)
	appendCase("candidate parent must match active version", "parent_version_mismatch", errors.Is(err, ErrParentVersionMismatch))
	activated, err := ActivateHarnessPointer(baseline, candidate, fullAdmission, baseline.StateVersion)
	appendCase("eligible candidate switches atomically", "cas_activation_succeeded", err == nil && activated.CurrentVersion == candidate.ArtifactVersion && activated.PreviousVersion == baseline.CurrentVersion && activated.StateVersion == 2)
	_, err = ActivateHarnessPointer(activated, otherCandidate, fullAdmission, baseline.StateVersion)
	appendCase("stale state version is rejected", "state_version_conflict", errors.Is(err, ErrPointerStateConflict))

	authority := newAcceptancePointerAuthority(baseline)
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, concurrentCandidate := range []ControlCandidate{candidate, otherCandidate} {
		concurrentCandidate := concurrentCandidate
		go func() {
			<-start
			_, transitionErr := authority.Activate(concurrentCandidate, fullAdmission, baseline.StateVersion)
			results <- transitionErr
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		transitionErr := <-results
		if transitionErr == nil {
			successes++
		} else if errors.Is(transitionErr, ErrPointerStateConflict) || errors.Is(transitionErr, ErrParentVersionMismatch) {
			conflicts++
		}
	}
	appendCase("concurrent activation has one winner", "single_cas_winner", successes == 1 && conflicts == 1 && authority.Snapshot().StateVersion == 2)

	rolledBack, err := RollbackHarnessPointer(activated, activated.StateVersion)
	appendCase("rollback restores previous version", "rollback_restored_previous", err == nil && rolledBack.CurrentVersion == baseline.CurrentVersion && rolledBack.StateVersion == 3 && rolledBack.PreviousVersion == "")
	_, err = RollbackHarnessPointer(rolledBack, rolledBack.StateVersion)
	appendCase("rollback cannot toggle repeatedly", "rollback_target_consumed", errors.Is(err, ErrRollbackUnavailable))

	report := ControlAcceptanceReport{
		SchemaVersion: ControlAcceptanceSchemaVersion, TransitionVersion: ControlTransitionVersion, Mode: ControlAcceptanceMode,
		GeneratedAt: generatedAt.UTC(), CaseCount: len(cases), ProductionWrites: 0, ProductionActivePointers: 0, Cases: cases,
		Guardrails:  []string{"offline_gate_required", "human_approval_required", "isolated_shadow_required", "safety_gate_required", "parent_hash_binding", "optimistic_cas", "single_step_rollback", "no_production_write_in_acceptance"},
		Limitations: []string{"本报告在内存中验证未来活动指针状态机，不创建生产活动指针，也不代表当前负收益候选已进入 Shadow。", "当前生产候选已在上游 Promotion Gate 被拒绝，因此真实激活与真实回滚路径按设计没有执行。"},
	}
	for _, item := range cases {
		if item.Passed {
			report.PassedCount++
		}
	}
	report.ReportSHA256 = controlAcceptanceHash(report)
	if err := ValidateControlAcceptanceReport(report); err != nil {
		return ControlAcceptanceReport{}, err
	}
	return report, nil
}

func ValidateControlAcceptanceReport(report ControlAcceptanceReport) error {
	if report.SchemaVersion != ControlAcceptanceSchemaVersion || report.TransitionVersion != ControlTransitionVersion || report.Mode != ControlAcceptanceMode ||
		report.GeneratedAt.IsZero() || report.CaseCount != 10 || report.PassedCount != report.CaseCount || report.ProductionWrites != 0 || report.ProductionActivePointers != 0 ||
		len(report.Cases) != report.CaseCount || len(report.Guardrails) != 8 || len(report.Limitations) != 2 || len(report.ReportSHA256) != 64 {
		return errors.New("harness control acceptance report envelope is invalid")
	}
	expectedReasons := []string{"human_approval_required", "offline_gate_required", "shadow_gate_required", "safety_gate_required", "parent_version_mismatch", "cas_activation_succeeded", "state_version_conflict", "single_cas_winner", "rollback_restored_previous", "rollback_target_consumed"}
	for index, item := range report.Cases {
		if !item.Passed || item.Name == "" || item.ReasonCode != expectedReasons[index] {
			return errors.New("harness control acceptance case is invalid")
		}
	}
	if controlAcceptanceHash(report) != report.ReportSHA256 {
		return errors.New("harness control acceptance report hash mismatch")
	}
	return nil
}

func controlAcceptanceHash(report ControlAcceptanceReport) string {
	report.ReportSHA256 = ""
	encoded, _ := json.Marshal(report)
	return digestBytes(encoded)
}

func controlBaselinePointer() ActiveHarnessPointer {
	return ActiveHarnessPointer{ArtifactType: "prompt_template", CurrentVersion: "grounded-answer-contract-v1", CurrentSHA256: digestString("grounded-answer-contract-v1"), StateVersion: 1, LastTransition: "baseline"}
}

func controlCandidate(version, sha string) ControlCandidate {
	baseline := controlBaselinePointer()
	return ControlCandidate{ArtifactType: baseline.ArtifactType, ArtifactVersion: version, ArtifactSHA256: sha, ParentVersion: baseline.CurrentVersion, ParentSHA256: baseline.CurrentSHA256}
}

func validControlPointer(pointer ActiveHarnessPointer) bool {
	return pointer.ArtifactType != "" && pointer.CurrentVersion != "" && len(pointer.CurrentSHA256) == 64 && pointer.StateVersion > 0
}

func validControlActivationParent(pointer ActiveHarnessPointer) bool {
	return pointer.ArtifactType != "" && pointer.CurrentVersion != "" && len(pointer.CurrentSHA256) == 64
}

func validControlCandidate(candidate ControlCandidate) bool {
	return candidate.ArtifactType != "" && candidate.ArtifactVersion != "" && len(candidate.ArtifactSHA256) == 64 && candidate.ParentVersion != "" && len(candidate.ParentSHA256) == 64
}

type acceptancePointerAuthority struct {
	mutex   sync.Mutex
	pointer ActiveHarnessPointer
}

func newAcceptancePointerAuthority(pointer ActiveHarnessPointer) *acceptancePointerAuthority {
	return &acceptancePointerAuthority{pointer: pointer}
}

func (authority *acceptancePointerAuthority) Activate(candidate ControlCandidate, admission ControlAdmission, expected uint64) (ActiveHarnessPointer, error) {
	authority.mutex.Lock()
	defer authority.mutex.Unlock()
	next, err := ActivateHarnessPointer(authority.pointer, candidate, admission, expected)
	if err != nil {
		return ActiveHarnessPointer{}, err
	}
	authority.pointer = next
	return next, nil
}

func (authority *acceptancePointerAuthority) Snapshot() ActiveHarnessPointer {
	authority.mutex.Lock()
	defer authority.mutex.Unlock()
	return authority.pointer
}
