// Package rcascoring owns evaluation truth; rcaexperiment must never import it.
package rcascoring

import (
	"GopherAI/internal/rcaexperiment"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed data/answers.json
var files embed.FS

type Answer struct {
	ID        string `json:"id"`
	Service   string `json:"service"`
	Fault     string `json:"fault"`
	Supported bool   `json:"supported"`
	Split     string `json:"split"`
}
type Score struct {
	Answer          Answer `json:"answer"`
	ServiceTop1     bool   `json:"service_top1"`
	ServiceTop3     bool   `json:"service_top3"`
	JointCorrect    bool   `json:"joint_correct"`
	Rejected        bool   `json:"rejected"`
	FalseAcceptance bool   `json:"false_acceptance"`
}

func Answers() ([]Answer, error) {
	raw, e := files.ReadFile("data/answers.json")
	if e != nil {
		return nil, e
	}
	var a []Answer
	e = json.Unmarshal(raw, &a)
	return a, e
}
func Check(r rcaexperiment.Diagnosis) (Score, error) {
	answers, e := Answers()
	if e != nil {
		return Score{}, e
	}
	for _, a := range answers {
		if a.ID == r.CaseID {
			s := Score{Answer: a, Rejected: r.Status != "matched_hypothesis"}
			if r.Status == "matched_hypothesis" && len(r.Candidates) > 0 {
				s.ServiceTop1 = r.Candidates[0].Service == a.Service
				s.JointCorrect = s.ServiceTop1 && r.Candidates[0].Fault == a.Fault
				for _, c := range r.Candidates {
					if c.Service == a.Service {
						s.ServiceTop3 = true
					}
				}
				s.FalseAcceptance = !a.Supported
			}
			return s, nil
		}
	}
	return Score{}, fmt.Errorf("answer not found")
}

type Metrics struct {
	Supported       int     `json:"supported"`
	Unsupported     int     `json:"unsupported"`
	Top1            int     `json:"top1"`
	Top3            int     `json:"top3"`
	Joint           int     `json:"joint"`
	InScopeRejected int     `json:"in_scope_rejected"`
	UnknownRejected int     `json:"unknown_rejected"`
	FalseAcceptance int     `json:"false_acceptance"`
	Errors          int     `json:"errors"`
	ElapsedMS       float64 `json:"elapsed_ms"`
}
type CaseResult struct {
	ID       string            `json:"id"`
	Strategy string            `json:"strategy"`
	Score    Score             `json:"score"`
	Run      rcaexperiment.Run `json:"run"`
	Error    string            `json:"error,omitempty"`
}
type Report struct {
	Version        string             `json:"version"`
	MatcherVersion string             `json:"matcher_version"`
	PolicySHA256   string             `json:"policy_sha256"`
	DatasetSHA256  string             `json:"dataset_sha256"`
	Revision       string             `json:"revision"`
	Split          string             `json:"split"`
	GeneratedAt    time.Time          `json:"generated_at"`
	Metrics        map[string]Metrics `json:"metrics"`
	Cases          []CaseResult       `json:"cases"`
	Limitations    []string           `json:"limitations"`
}

// Compact removes duplicate telemetry already present in the hash-bound input
// artifact. Predictions, evidence, tool statuses and scores are retained.
func (r *Report) Compact() {
	for i := range r.Cases {
		r.Cases[i].Run.Observation.Services = nil
		for j := range r.Cases[i].Run.ToolCalls {
			r.Cases[i].Run.ToolCalls[j].Data = nil
		}
		for j := range r.Cases[i].Run.Diagnosis.References {
			r.Cases[i].Run.Diagnosis.References[j].Observation = rcaexperiment.Service{}
		}
	}
}

func Evaluate(ctx context.Context, d *rcaexperiment.Dataset, split string) (Report, error) {
	if split != "development" && split != "holdout" {
		return Report{}, fmt.Errorf("invalid evaluation split")
	}
	r := Report{Version: "rcaeval-known-report-v1", MatcherVersion: rcaexperiment.MatcherVersion, DatasetSHA256: d.SHA256, Revision: d.Revision, Split: split, GeneratedAt: time.Now().UTC(), Metrics: map[string]Metrics{}, Cases: []CaseResult{}, Limitations: []string{"同一服务/故障配方的不同实验运行；仅检查已知模式复现，不证明未知故障或跨系统泛化。", "只有两个受支持服务，Top-3 区分力有限；优先看 Top-1 与服务/类型联合正确数。", "窗口最早/最晚三分之一聚合；参考窗口健康是显式假设，不使用注入时间或正确服务选取特征。", "本报告运行在本地，不是服务器端耗时/内存测试；不调用 LLM，不验证修复成功率。"}}
	r.PolicySHA256 = d.PolicySHA256
	for _, c := range d.Catalog {
		if c.Split != split {
			continue
		}
		for _, strategy := range []string{"legacy", "feature_only", "case_based"} {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			run, err := rcaexperiment.Execute(ctx, d, c.ID, strategy, "offline-evaluator", nil, nil)
			if err != nil {
				run.Diagnosis = rcaexperiment.Diagnosis{CaseID: c.ID, Status: "error"}
			}
			score, e := Check(run.Diagnosis)
			if e != nil {
				return r, e
			}
			entry := CaseResult{ID: c.ID, Strategy: strategy, Score: score, Run: run}
			m := r.Metrics[strategy]
			if err != nil {
				entry.Error = err.Error()
				m.Errors++
			}
			if score.Answer.Supported {
				m.Supported++
				if score.ServiceTop1 {
					m.Top1++
				}
				if score.ServiceTop3 {
					m.Top3++
				}
				if score.JointCorrect {
					m.Joint++
				}
				if score.Rejected {
					m.InScopeRejected++
				}
			} else {
				m.Unsupported++
				if score.Rejected && err == nil {
					m.UnknownRejected++
				}
				if score.FalseAcceptance {
					m.FalseAcceptance++
				}
			}
			m.ElapsedMS += run.ElapsedMS
			r.Metrics[strategy] = m
			r.Cases = append(r.Cases, entry)
		}
	}
	return r, nil
}
