// Package rcaexperiment diagnoses a bounded public benchmark using observations
// and reference cases only. Test labels live in internal/rcascoring, not here.
package rcaexperiment

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

//go:embed data/observations.json data/references.json matcher.go
var artifacts embed.FS

type Metric struct {
	ID              string    `json:"id"`
	Column          string    `json:"column"`
	Reference       float64   `json:"reference"`
	Current         float64   `json:"current"`
	Ratio           float64   `json:"ratio"`
	LogChange       float64   `json:"log_change"`
	ReferenceCV     float64   `json:"reference_cv"`
	MissingFraction float64   `json:"missing_fraction"`
	ReferenceCount  int       `json:"reference_count"`
	CurrentCount    int       `json:"current_count"`
	Unit            string    `json:"unit"`
	Sparkline       []float64 `json:"sparkline"`
}
type LogExample struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}
type Logs struct {
	ID             string       `json:"id"`
	ReferenceCount int          `json:"reference_count"`
	CurrentCount   int          `json:"current_count"`
	KeywordMatches int          `json:"keyword_matches"`
	Examples       []LogExample `json:"examples"`
}
type Traces struct {
	ID                 string  `json:"id"`
	ReferenceSpans     int     `json:"reference_spans"`
	CurrentSpans       int     `json:"current_spans"`
	ReferenceP95       float64 `json:"reference_p95"`
	CurrentP95         float64 `json:"current_p95"`
	NonzeroStatusCount int     `json:"nonzero_status_count"`
	SampleSpan         string  `json:"sample_span"`
	SampleTrace        string  `json:"sample_trace"`
	SampleOperation    string  `json:"sample_operation"`
	SampleStartMS      int64   `json:"sample_start_ms"`
	Unit               string  `json:"unit"`
}
type Service struct {
	Name    string            `json:"name"`
	Metrics map[string]Metric `json:"metrics"`
	Logs    Logs              `json:"logs"`
	Traces  Traces            `json:"traces"`
}
type Source struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Observation struct {
	ID           string    `json:"id"`
	Start        int64     `json:"start"`
	End          int64     `json:"end"`
	ReferenceEnd int64     `json:"reference_end"`
	CurrentStart int64     `json:"current_start"`
	MetricRows   int       `json:"metric_rows"`
	LogRows      int       `json:"log_rows"`
	TraceRows    int       `json:"trace_rows"`
	Sources      []Source  `json:"sources"`
	Services     []Service `json:"services"`
	Warnings     []string  `json:"warnings"`
}
type Case struct {
	ID    string `json:"id"`
	Split string `json:"split"`
	Title string `json:"title"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
}
type Reference struct {
	ID          string  `json:"id"`
	Service     string  `json:"service"`
	Fault       string  `json:"fault"`
	Resolution  string  `json:"resolution"`
	Provenance  string  `json:"provenance"`
	Observation Service `json:"observation"`
	Similarity  float64 `json:"similarity"`
}
type Dataset struct {
	Version      string        `json:"version"`
	Extractor    string        `json:"extractor"`
	Revision     string        `json:"revision"`
	Catalog      []Case        `json:"catalog"`
	Observations []Observation `json:"observations"`
	References   []Reference   `json:"references"`
	SHA256       string        `json:"sha256"`
	PolicySHA256 string        `json:"policy_sha256"`
}

func Load() (*Dataset, error) {
	raw, err := artifacts.ReadFile("data/observations.json")
	if err != nil {
		return nil, err
	}
	refs, err := artifacts.ReadFile("data/references.json")
	if err != nil {
		return nil, err
	}
	if len(raw)+len(refs) > 32<<20 {
		return nil, fmt.Errorf("dataset exceeds memory budget")
	}
	var d Dataset
	if err = json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(refs, &d.References); err != nil {
		return nil, err
	}
	if len(d.Catalog) != 27 || len(d.Observations) != 27 || len(d.References) != 6 {
		return nil, fmt.Errorf("unexpected experiment population")
	}
	seen := map[string]bool{}
	for _, o := range d.Observations {
		if seen[o.ID] || o.Start >= o.End || len(o.Services) > 64 {
			return nil, fmt.Errorf("invalid observation")
		}
		seen[o.ID] = true
		for _, s := range o.Services {
			for _, m := range s.Metrics {
				if math.IsNaN(m.LogChange) || math.IsInf(m.LogChange, 0) || m.MissingFraction < 0 || m.MissingFraction > 1 {
					return nil, fmt.Errorf("invalid metric")
				}
			}
		}
	}
	for i, r := range d.References {
		c, ok := d.Case(r.ID)
		if !ok || c.Split != "reference" || !KnownService(r.Service) || FaultName(r.Fault) == "" {
			return nil, fmt.Errorf("invalid reference membership")
		}
		o, _ := d.Observation(r.ID)
		s, ok := FindService(o, r.Service)
		if !ok {
			return nil, fmt.Errorf("reference service missing")
		}
		d.References[i].Observation = s
	}
	h := sha256.New()
	h.Write(raw)
	h.Write(refs)
	d.SHA256 = hex.EncodeToString(h.Sum(nil))
	policy, err := artifacts.ReadFile("matcher.go")
	if err != nil {
		return nil, err
	}
	policyHash := sha256.Sum256(policy)
	d.PolicySHA256 = hex.EncodeToString(policyHash[:])
	return &d, nil
}
func (d *Dataset) Observation(id string) (Observation, bool) {
	for _, o := range d.Observations {
		if o.ID == id {
			return o, true
		}
	}
	return Observation{}, false
}
func (d *Dataset) Case(id string) (Case, bool) {
	for _, c := range d.Catalog {
		if c.ID == id {
			return c, true
		}
	}
	return Case{}, false
}
func FindService(o Observation, name string) (Service, bool) {
	for _, s := range o.Services {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}
func KnownService(s string) bool { return s == "checkoutservice" || s == "currencyservice" }
func FaultName(f string) string {
	switch f {
	case "cpu":
		return "CPU 压力"
	case "mem":
		return "内存压力"
	case "delay":
		return "网络延迟"
	}
	return ""
}
