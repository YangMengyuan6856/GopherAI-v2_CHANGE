package main

import (
	"GopherAI/internal/rcaexperiment"
	"GopherAI/internal/rcascoring"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	split := flag.String("split", "development", "development or holdout")
	output := flag.String("output", "", "report output file (required)")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "-output is required")
		os.Exit(2)
	}
	d, err := rcaexperiment.Load()
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := rcascoring.Evaluate(ctx, d, *split)
	if err != nil {
		panic(err)
	}
	r.Compact()
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(*output, append(raw, '\n'), 0600); err != nil {
		panic(err)
	}
	summary, _ := json.MarshalIndent(r.Metrics, "", "  ")
	fmt.Println(string(summary))
	fmt.Println("Report:", *output, "dataset SHA:", d.SHA256)
}
