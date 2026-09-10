// Runs real-model replays. Never overwrites the previous deterministic report.
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
	ID    string           `json:"id"`
	Title string           `json:"title"`
	Run   rcaagent.Run     `json:"run"`
	Score rcascoring.Score `json:"score"`
	Valid bool             `json:"evaluation_valid"`
}
type report struct {
	Version              string         `json:"version"`
	DatasetSHA256        string         `json:"dataset_sha256"`
	PromptSHA256         string         `json:"prompt_sha256"`
	ImplementationSHA256 string         `json:"implementation_sha256"`
	Split                string         `json:"split"`
	EvaluationKind       string         `json:"evaluation_kind"`
	GeneratedAt          time.Time      `json:"generated_at"`
	Cases                []row          `json:"cases"`
	Metrics              map[string]int `json:"metrics"`
	Limitations          []string       `json:"limitations"`
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
	r := report{Version: rcaagent.Version, DatasetSHA256: d.SHA256, PromptSHA256: rcaagent.PromptHash(), ImplementationSHA256: rcaagent.ImplementationHash(), Split: split, EvaluationKind: "previously_exposed_case_replay_not_new_blind_test", GeneratedAt: time.Now().UTC(), Cases: []row{}, Metrics: map[string]int{},
		Limitations: []string{"这是此前已查看案例的模型回放，不是新盲测或生产准确率。", "有限指标/日志/调用链摘要；只支持2个服务和3类已知模式；不执行修复。", "模型调用错误、预算停止不计为正确拒答；保留全部尝试，不能挑最好一次。", "模型自主选工具不意味着诊断必定正确；引用存在也不证明因果。"}}
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
		entry := row{ID: c.ID, Title: c.Title, Run: out, Score: score, Valid: out.Agent.Completed}
		if e = json.NewEncoder(checkpoints).Encode(entry); e != nil {
			return e
		}
		if e = checkpoints.Sync(); e != nil {
			return e
		}
		r.Cases = append(r.Cases, entry)
		r.Metrics["attempted"]++
		if score.Answer.Supported {
			r.Metrics["supported"]++
		} else {
			r.Metrics["unsupported"]++
		}
		if !entry.Valid {
			r.Metrics["execution_failed"]++
		} else {
			r.Metrics["completed"]++
			if score.Answer.Supported && score.JointCorrect {
				r.Metrics["joint_correct"]++
			}
			if score.Answer.Supported && score.ServiceTop1 {
				r.Metrics["service_top1"]++
			}
			if !score.Answer.Supported && score.Rejected {
				r.Metrics["unknown_rejected"]++
			}
			if score.FalseAcceptance {
				r.Metrics["false_acceptance"]++
			}
		}
		r.Metrics["model_calls"] += out.Diagnosis.ModelCalls
		r.Metrics["input_tokens"] += out.Agent.InputTokens
		r.Metrics["output_tokens"] += out.Agent.OutputTokens
		fmt.Printf("%s completed=%t stop=%s model_calls=%d tools=%d joint=%t false_acceptance=%t\n", c.Title, entry.Valid, out.Agent.StopReason, out.Diagnosis.ModelCalls, len(out.ToolCalls), score.JointCorrect, score.FalseAcceptance)
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
