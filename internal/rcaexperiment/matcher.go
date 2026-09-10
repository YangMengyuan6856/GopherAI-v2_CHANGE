package rcaexperiment

import (
	"fmt"
	"math"
	"sort"
)

const MatcherVersion = "known-pattern-matcher-v2"
const MinSimilarity = 0.64

type Evidence struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Statement string `json:"statement"`
}
type FollowUp struct {
	Check    string `json:"check"`
	Supports string `json:"supports"`
	Weakens  string `json:"weakens"`
	Executed bool   `json:"executed"`
}
type Candidate struct {
	Service     string     `json:"service"`
	Fault       string     `json:"fault"`
	Cause       string     `json:"cause"`
	Score       float64    `json:"score"`
	Similarity  float64    `json:"similarity"`
	ReferenceID string     `json:"reference_id,omitempty"`
	Evidence    []Evidence `json:"evidence"`
	Differences []string   `json:"differences"`
	FollowUps   []FollowUp `json:"follow_ups"`
}
type Diagnosis struct {
	CaseID       string      `json:"case_id"`
	Version      string      `json:"version"`
	Strategy     string      `json:"strategy"`
	Status       string      `json:"status"`
	Summary      string      `json:"summary"`
	Candidates   []Candidate `json:"candidates"`
	References   []Reference `json:"references"`
	Warnings     []string    `json:"warnings"`
	ModelCalls   int         `json:"model_calls"`
	RepairStatus string      `json:"repair_status"`
}

var dimensions = []struct {
	Name   string
	Weight float64
}{{"cpu", 1}, {"mem", 1.4}, {"latency-90", 1}, {"socket", .35}, {"workload", .3}}

func change(s Service, name string) float64 { return s.Metrics[name].LogChange }
func similarity(a, b Service, fault string) float64 {
	distance, weight := 0.0, 0.0
	for _, dim := range dimensions {
		x, xok := a.Metrics[dim.Name]
		y, yok := b.Metrics[dim.Name]
		if !xok || !yok || x.MissingFraction > .2 {
			continue
		}
		delta := math.Abs(x.LogChange-y.LogChange) / math.Max(1, math.Abs(y.LogChange))
		// Same known pattern may recur at a higher intensity. Preserve secondary
		// dimensions; do not require identical injection amplitudes.
		if dim.Name == primaryMetric(fault) && y.LogChange > 0 && x.LogChange > y.LogChange {
			delta *= .2
		}
		distance += dim.Weight * math.Min(4, delta)
		weight += dim.Weight
	}
	if weight < 3 {
		return 0
	}
	return math.Exp(-distance / weight)
}

// Retrieval uses observation features, never case ID, split or test truth.
func SearchReferences(o Observation, refs []Reference) []Reference {
	result := make([]Reference, 0, len(refs))
	for _, r := range refs {
		s, ok := FindService(o, r.Service)
		if !ok || r.ID == o.ID {
			continue
		}
		if _, eligible := eligibleReference(s, r); !eligible {
			continue
		}
		r.Similarity = similarity(s, r.Observation, r.Fault)
		result = append(result, r)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Similarity == result[j].Similarity {
			return result[i].ID < result[j].ID
		}
		return result[i].Similarity > result[j].Similarity
	})
	if len(result) > 3 {
		result = result[:3]
	}
	return result
}

func eligible(s Service, fault string) (float64, bool) {
	return eligibleAt(s, fault, 1)
}
func primaryMetric(fault string) string {
	if fault == "delay" {
		return "latency-90"
	}
	return fault
}
func eligibleReference(s Service, r Reference) (float64, bool) {
	threshold := math.Max(.25, math.Min(1, change(r.Observation, primaryMetric(r.Fault))*.6))
	strength, ok := eligibleAt(s, r.Fault, threshold)
	if r.Fault == "cpu" && change(s, "latency-90") > math.Max(2, change(r.Observation, "latency-90")+1.5) {
		ok = false
	}
	return strength, ok
}
func eligibleAt(s Service, fault string, threshold float64) (float64, bool) {
	for _, name := range []string{"cpu", "mem", "latency-90"} {
		m, ok := s.Metrics[name]
		if !ok || m.MissingFraction > .2 || m.ReferenceCount < 20 || m.CurrentCount < 20 {
			return 0, false
		}
	}
	cpu, mem, lat := change(s, "cpu"), change(s, "mem"), change(s, "latency-90")
	switch fault {
	case "mem":
		return mem, mem >= threshold
	case "cpu":
		return cpu, cpu >= threshold && mem < 1.0
	case "delay":
		return lat, lat >= threshold && cpu < 1.0 && mem < 1.0
	}
	return 0, false
}

func Diagnose(o Observation, refs []Reference, strategy string) Diagnosis {
	r := Diagnosis{CaseID: o.ID, Version: MatcherVersion, Strategy: strategy, Status: "insufficient_evidence", Candidates: []Candidate{}, References: []Reference{}, Warnings: append([]string{}, o.Warnings...), RepairStatus: "not_executed"}
	r.Warnings = append(r.Warnings, "只支持指定服务的 CPU/内存/网络延迟模式；相似度不是根因概率。", "未知故障可能出现相似现象；此输出是候选假设，不是根因确认。")
	if strategy == "case_based" {
		r.References = refs
	}
	for _, s := range o.Services {
		if !KnownService(s.Name) {
			continue
		}
		for _, fault := range []string{"cpu", "mem", "delay"} {
			strength, ok := eligible(s, fault)
			var matched *Reference
			if strategy == "case_based" {
				for i := range refs {
					if refs[i].Service == s.Name && refs[i].Fault == fault && (matched == nil || refs[i].Similarity > matched.Similarity) {
						matched = &refs[i]
					}
				}
				if matched == nil || matched.Similarity < MinSimilarity {
					continue
				}
				strength, ok = eligibleReference(s, *matched)
			}
			if !ok {
				continue
			}
			c := Candidate{Service: s.Name, Fault: fault, Cause: FaultName(fault), Score: math.Min(strength/8, 1), Evidence: []Evidence{}, Differences: []string{}, FollowUps: followUps(fault)}
			if strategy == "case_based" {
				c.ReferenceID = matched.ID
				c.Similarity = matched.Similarity
				c.Score = .2*c.Similarity + .8*c.Score
				for _, dim := range dimensions {
					x, xok := s.Metrics[dim.Name]
					y, yok := matched.Observation.Metrics[dim.Name]
					if xok && yok {
						c.Differences = append(c.Differences, fmt.Sprintf("%s：当前 %.2f×，参考案例 %.2f×（各自与参考窗口比较）", dim.Name, x.Ratio, y.Ratio))
					}
				}
			}
			// Within this limited resource-fault scope, local resource evidence
			// outranks latency alone, which can be an upstream propagated symptom.
			// Both B and C use this same heuristic; it is not a causal proof.
			if fault != "delay" {
				c.Score += .5
			}
			for _, name := range []string{"cpu", "mem", "latency-90", "workload", "socket"} {
				if m, ok := s.Metrics[name]; ok {
					c.Evidence = append(c.Evidence, Evidence{m.ID, "metric", fmt.Sprintf("%s：参考 %.4g → 当前 %.4g，%.2f×（原始单位）", m.Column, m.Reference, m.Current, m.Ratio)})
				}
			}
			if s.Logs.ID != "" {
				c.Evidence = append(c.Evidence, Evidence{s.Logs.ID, "logs", fmt.Sprintf("当前窗口 %d 条日志，错误关键词匹配 %d 条；关键词命中不等于根因。", s.Logs.CurrentCount, s.Logs.KeywordMatches)})
			}
			if s.Traces.ID != "" {
				c.Evidence = append(c.Evidence, Evidence{s.Traces.ID, "traces", fmt.Sprintf("调用链 P95：%.4g → %.4g，当前 %d 个 span（原始单位）。", s.Traces.ReferenceP95, s.Traces.CurrentP95, s.Traces.CurrentSpans)})
			}
			r.Candidates = append(r.Candidates, c)
		}
	}
	sort.SliceStable(r.Candidates, func(i, j int) bool { return r.Candidates[i].Score > r.Candidates[j].Score })
	if len(r.Candidates) > 3 {
		r.Candidates = r.Candidates[:3]
	}
	if len(r.Candidates) > 0 {
		r.Status = "matched_hypothesis"
		c := r.Candidates[0]
		r.Summary = fmt.Sprintf("优先排查 %s 的%s模式；已列出当前观测及待确认项，尚未确认根因或执行修复。", c.Service, c.Cause)
	} else {
		r.Summary = "当前观测不足以可靠匹配已支持的故障模式。请补充稳定的正常窗口、服务资源限制或网络错误明细；不要据此执行修复。"
	}
	return r
}
func followUps(f string) []FollowUp {
	switch f {
	case "cpu":
		return []FollowUp{{"检查目标服务 CPU 配额、节流时间及同期负载", "CPU/节流持续增加且发生时间与故障相符", "CPU 正常或异常主要由上游/下游传播", false}, {"核查近期压测、流量变化或计算密集变更", "发现与当前窗口相关的负载或代码变化", "没有相应变化，需要继续排查", false}}
	case "mem":
		return []FollowUp{{"核对 RSS/工作集、内存上限和分配/GC 情况", "内存持续升高，接近限制或出现分配压力", "只有 CPU 上升，内存缺乏持续异常", false}, {"排查对象积压、缓存增长或内存压力注入", "增长与当前事故时间相关", "短暂波动或健康窗口本来不稳定", false}}
	default:
		return []FollowUp{{"补查目标服务网络 RTT、丢包/重传与调用依赖耗时", "网络耗时增加，与延迟窗口及依赖关系吻合", "CPU/内存压力或下游处理耗时更能解释现象", false}, {"区分纯延迟、丢包、socket 限制或连接池等待", "存在可定位的网络延迟证据", "只看到请求变慢，不能区分具体原因", false}}
	}
}
