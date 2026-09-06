package perfeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func WriteReports(root string, report Report) error {
	if err := Validate(report, true); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	markdown := []byte(fmt.Sprintf("# GopherAI 性能验收报告\n\n- Release：`%s`\n- Report SHA-256：`%s`\n- 技术门：`%t`\n- 冷路径 P50/P95/P99：`%.3f / %.3f / %.3f ms`\n- 热路径 P50/P95/P99：`%.3f / %.3f / %.3f ms`\n- 热路径 TTFT P50/P95/P99：`%.3f / %.3f / %.3f ms`\n- 热路径成功率：`%.2f%%`\n- 热路径 Token/100 成功请求：`%.2f`\n\n> %s\n> %s\n", report.Release.ID, report.ReportSHA256, report.Gates.TechnicalPassed, report.Cold.TotalLatency.P50MS, report.Cold.TotalLatency.P95MS, report.Cold.TotalLatency.P99MS, report.Hot.TotalLatency.P50MS, report.Hot.TotalLatency.P95MS, report.Hot.TotalLatency.P99MS, report.Hot.TTFT.P50MS, report.Hot.TTFT.P95MS, report.Hot.TTFT.P99MS, report.Hot.SuccessRate*100, report.Hot.TokensPer100, report.Limitations[0], report.Limitations[1]))
	releaseRoot := filepath.Join(root, report.Release.ID)
	if err := os.MkdirAll(releaseRoot, 0700); err != nil {
		return err
	}
	for _, target := range []struct {
		path string
		data []byte
	}{
		{filepath.Join(releaseRoot, "report.json"), encoded}, {filepath.Join(releaseRoot, "report.md"), markdown},
		{filepath.Join(root, "latest.json"), encoded}, {filepath.Join(root, "latest.md"), markdown},
	} {
		if err := atomicWrite(target.path, target.data); err != nil {
			return err
		}
	}
	return nil
}

func LoadReport(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	if len(data) > 2*1024*1024 {
		return Report{}, fmt.Errorf("performance report exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Report{}, fmt.Errorf("performance report contains trailing JSON")
	}
	if err := Validate(report, true); err != nil {
		return Report{}, err
	}
	return report, nil
}

func atomicWrite(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
