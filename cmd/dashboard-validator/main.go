package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"GopherAI/internal/observability"
)

func main() {
	root := flag.String("root", ".", "repository or release root containing deploy/observability/grafana")
	flag.Parse()
	contract, err := observability.ValidateGrafanaAssets(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Grafana dashboard validation failed")
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(contract); err != nil {
		fmt.Fprintln(os.Stderr, "Grafana dashboard report encoding failed")
		os.Exit(1)
	}
}
