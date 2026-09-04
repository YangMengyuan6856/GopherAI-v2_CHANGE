package toolruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedLogSignatureToolReturnsOnlyMatchingRedactedEvidence(t *testing.T) {
	root := t.TempDir()
	contents := strings.Join([]string{
		"service started normally",
		"ERROR redis NOAUTH password=super-secret email=alice@example.com",
		"WARN amqp://guest:guest@rabbitmq:5672 retry after timeout Authorization: Bearer eyJabcdefghijk.abcdefghijk.signature",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "backend.log"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewBoundedLogSignatureTool(root)
	output, err := tool.Execute(context.Background(), map[string]any{"service": "backend", "signature": "auth"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := output.Data.(PublicLogSignatureSnapshot)
	if snapshot.MatchedLines != 1 || snapshot.ReturnedLines != 1 || len(output.EvidenceRefs) != 1 || !strings.HasPrefix(output.EvidenceRefs[0], "log-signature:backend:auth:") {
		t.Fatalf("unexpected snapshot: %+v evidence=%v", snapshot, output.EvidenceRefs)
	}
	excerpt := snapshot.Matches[0].Excerpt
	for _, secret := range []string{"super-secret", "alice@example.com"} {
		if strings.Contains(excerpt, secret) {
			t.Fatalf("secret was not redacted: %s", excerpt)
		}
	}
	if !strings.Contains(excerpt, "[REDACTED]") || !strings.Contains(excerpt, "[REDACTED_EMAIL]") || len(snapshot.Matches[0].LineHash) != 64 {
		t.Fatalf("redaction or hash missing: %+v", snapshot.Matches[0])
	}
}

func TestBoundedLogSignatureToolKeepsLatestTwentyAndMarksTruncation(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 25)
	for index := range lines {
		lines[index] = "ERROR fixture line " + strings.Repeat("x", index)
	}
	if err := os.WriteFile(filepath.Join(root, "backend.log"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := NewBoundedLogSignatureTool(root).Execute(context.Background(), map[string]any{"service": "backend", "signature": "error"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := output.Data.(PublicLogSignatureSnapshot)
	if snapshot.MatchedLines != 25 || snapshot.ReturnedLines != 20 || !snapshot.Truncated || snapshot.Matches[0].ScannedLine != 6 {
		t.Fatalf("bounded latest matches failed: %+v", snapshot)
	}
}

func TestBoundedLogSignatureToolRejectsUnknownSourceAndEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	tool := NewBoundedLogSignatureTool(root)
	if _, err := tool.Execute(context.Background(), map[string]any{"service": "../../etc", "signature": "error"}); err == nil {
		t.Fatal("unknown source was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("ERROR secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "backend.log")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"service": "backend", "signature": "error"}); err == nil {
		t.Fatalf("escaping symlink was not rejected: %v", err)
	}
}
