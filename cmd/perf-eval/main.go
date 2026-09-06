package main

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/perfeval"
	"GopherAI/model"
	"GopherAI/utils/myjwt"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"gorm.io/gorm"
)

const probeUser = "__perf_probe__"

func main() {
	hotRequests := flag.Int("hot-requests", 10, "measured hot-path requests (1..48; cold plus warm-up keep total at 50 or less)")
	hotConcurrency := flag.Int("hot-concurrency", 2, "hot-path concurrency (1..5)")
	timeoutSeconds := flag.Int("request-timeout-seconds", 120, "per-request timeout (1..120)")
	profileSeconds := flag.Int("profile-seconds", 5, "CPU profile duration (1..15)")
	flag.Parse()
	if err := mysql.InitMysql(); err != nil {
		fail(err)
	}
	token, err := myjwt.GenerateEphemeralToken(0, probeUser)
	if err != nil {
		fail(err)
	}
	config := perfeval.Config{HotRequests: *hotRequests, HotConcurrency: *hotConcurrency, RequestTimeout: time.Duration(*timeoutSeconds) * time.Second, ProfileSeconds: *profileSeconds, OutputRoot: "/root/GopherAI_Runtime/perf", Token: token, Now: time.Now, Cleanup: cleanupProbeData}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	report, err := perfeval.NewRunner().Run(ctx, config)
	if err != nil {
		fail(err)
	}
	if err := perfeval.WriteReports(config.OutputRoot, report); err != nil {
		fail(err)
	}
	fmt.Printf("report=%s release=%s passed=%t hot_p95_ms=%.3f hot_ttft_p95_ms=%.3f\n", report.ReportSHA256, report.Release.ID, report.Gates.TechnicalPassed, report.Hot.TotalLatency.P95MS, report.Hot.TTFT.P95MS)
	if !report.Gates.TechnicalPassed {
		os.Exit(2)
	}
}

func cleanupProbeData(ctx context.Context) error {
	hash := sha256.Sum256([]byte(probeUser))
	return mysql.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_name = ?", probeUser).Delete(&model.Message{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id_hash = ?", hex.EncodeToString(hash[:])).Delete(&model.AgentRun{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Where("user_name = ?", probeUser).Delete(&model.Session{}).Error
	})
}

func fail(err error) { fmt.Fprintln(os.Stderr, "performance acceptance failed:", err); os.Exit(1) }
