package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseOptionsAcceptsBoundedReleaseIdentity(t *testing.T) {
	var stderr bytes.Buffer
	options, err := parseOptions([]string{
		"-release-id", "20260907050533-740abc053bde",
		"-git-sha", strings.Repeat("a", 40),
		"-root", ".", "-report", "report.json", "-timeout", "8s",
	}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if options.releaseID != "20260907050533-740abc053bde" || options.gitSHA != strings.Repeat("a", 40) || options.timeout != 8*time.Second {
		t.Fatalf("unexpected options: %+v", options)
	}
}

func TestParseOptionsRejectsUnsafeOrIncompleteInput(t *testing.T) {
	for _, args := range [][]string{
		{"-release-id", "../release", "-git-sha", strings.Repeat("a", 40)},
		{"-release-id", "release", "-git-sha", "abc"},
		{"-release-id", "release", "-git-sha", strings.Repeat("a", 40), "-timeout", "2m"},
		{"-release-id", "release", "-git-sha", strings.Repeat("a", 40), "extra"},
	} {
		var stderr bytes.Buffer
		if _, err := parseOptions(args, &stderr); err == nil {
			t.Fatalf("expected invalid options for %v", args)
		}
	}
}
