// Runs real-model evaluations into new files. Once the fixed holdout artifact is
// published, later holdout runs are explicitly labeled as exposed replays.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	rcaagentplatform "GopherAI/internal/platform/rcaagent"
	"GopherAI/internal/rcaagent"
	"GopherAI/internal/rcaexperiment"
	"GopherAI/internal/rcascoring"
)

type row struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Run           rcaagent.Run     `json:"run"`
	Score         rcascoring.Score `json:"score"`
	Valid         bool             `json:"evaluation_valid"`
	EvidenceValid bool             `json:"evidence_valid"`
}
type metrics struct {
	Attempted       int   `json:"attempted"`
	Completed       int   `json:"completed"`
	ExecutionFailed int   `json:"execution_failed"`
	ServiceTop1     int   `json:"service_top1"`
	JointCorrect    int   `json:"joint_correct"`
	EvidenceValid   int   `json:"evidence_valid"`
	ModelCalls      int   `json:"model_calls"`
	ToolCalls       int   `json:"tool_calls"`
	InputTokens     int   `json:"input_tokens"`
	OutputTokens    int   `json:"output_tokens"`
	ElapsedMSTotal  int64 `json:"elapsed_ms_total"`
}
type report struct {
	Version              string    `json:"version"`
	DatasetSHA256        string    `json:"dataset_sha256"`
	PromptSHA256         string    `json:"prompt_sha256"`
	ImplementationSHA256 string    `json:"implementation_sha256"`
	Split                string    `json:"split"`
	EvaluationKind       string    `json:"evaluation_kind"`
	GeneratedAt          time.Time `json:"generated_at"`
	Cases                []row     `json:"cases"`
	Metrics              metrics   `json:"metrics"`
	Limitations          []string  `json:"limitations"`
}

func main() {
	split := flag.String("split", "development", "development or holdout (previously exposed replay)")
	limit := flag.Int("limit", 0, "maximum cases; 0 means complete split")
	output := flag.String("output", "", "new output path; existing files are never overwritten")
	flag.Parse()
	if err := run(*split, *limit, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(split string, limit int, path string) error {
	if (split != "development" && split != "holdout") || path == "" || limit < 0 {
		return fmt.Errorf("invalid arguments")
	}
	d, err := rcaexperiment.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	a, err := rcaagentplatform.NewDefaultAgent(ctx)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	checkpoints, err := os.OpenFile(path+".runs.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer checkpoints.Close()
	kind := reportKind(split)
	r := report{Version: rcaagent.Version, DatasetSHA256: d.SHA256, PromptSHA256: rcaagent.PromptHash(), ImplementationSHA256: rcaagent.ImplementationHash(), Split: split, EvaluationKind: kind, GeneratedAt: time.Now().UTC(), Cases: []row{},
		Limitations: []string{"公开 RCAEval RE2-OB 的固定案例评测；标准答案与模型工具隔离，但不宣称生产准确率或绝对盲测。", "仅覆盖2个服务和CPU、内存、延迟3类已知故障；不评价未知故障、跨系统泛化或修复成功率。", "每个案例只运行一次，模型错误、超时和预算停止都保留在分母。", "模型自主选工具只证明有界排查流程可运行；引用有效仍不等于因果已经确认。"}}
	for _, c := range d.Catalog {
		if c.Split != split {
			continue
		}
		if limit > 0 && len(r.Cases) >= limit {
			break
		}
		out, e := a.Execute(ctx, d, c.ID, "offline-rca-agent-replay", nil, nil)
		if e != nil {
			return fmt.Errorf("replay initialization failed")
		}
		score, e := rcascoring.Check(out.Diagnosis)
		if e != nil {
			return e
		}
		// Raw current observations remain in the immutable dataset; keep actual
		// selected tool results in Steps for independent trajectory inspection.
		out.Observation.Services = nil
		for i := range out.ToolCalls {
			out.ToolCalls[i].Data = nil
		}
		entry := row{ID: c.ID, Title: c.Title, Run: out, Score: score, Valid: out.Agent.Completed, EvidenceValid: rcaagent.EvidenceContractSatisfied(out)}
		if e = json.NewEncoder(checkpoints).Encode(entry); e != nil {
			return e
		}
		if e = checkpoints.Sync(); e != nil {
			return e
		}
		r.Cases = append(r.Cases, entry)
		r.Metrics.Attempted++
		if !entry.Valid {
			r.Metrics.ExecutionFailed++
		} else {
			r.Metrics.Completed++
			if score.JointCorrect {
				r.Metrics.JointCorrect++
			}
			if score.ServiceTop1 {
				r.Metrics.ServiceTop1++
			}
			if entry.EvidenceValid {
				r.Metrics.EvidenceValid++
			}
		}
		r.Metrics.ModelCalls += out.Diagnosis.ModelCalls
		r.Metrics.ToolCalls += len(out.ToolCalls)
		r.Metrics.InputTokens += out.Agent.InputTokens
		r.Metrics.OutputTokens += out.Agent.OutputTokens
		r.Metrics.ElapsedMSTotal += int64(out.ElapsedMS)
		fmt.Printf("%s completed=%t stop=%s model_calls=%d tools=%d service_top1=%t joint=%t evidence_valid=%t\n", c.Title, entry.Valid, out.Agent.StopReason, out.Diagnosis.ModelCalls, len(out.ToolCalls), score.ServiceTop1, score.JointCorrect, entry.EvidenceValid)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(r); err != nil {
		return err
	}
	b, _ := json.Marshal(r.Metrics)
	fmt.Println(string(b))
	return f.Sync()
}

func reportKind(split string) string {
	if split == "holdout" {
		return "previously_exposed_case_replay"
	}
	return "development_iteration"
}
