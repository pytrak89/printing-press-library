// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/social-and-messaging/x-twitter/internal/store"
	"github.com/spf13/cobra"
)

func TestExpandedWorkflowCommandsAreWired(t *testing.T) {
	root := RootCmd()
	for _, path := range [][]string{
		{"monitor", "create"},
		{"monitor", "run"},
		{"brief"},
		{"account", "snapshot"},
		{"url", "mentions"},
		{"performance", "snapshot"},
		{"performance", "backfill"},
		{"performance", "analyze"},
		{"timeline", "export"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
			t.Fatalf("RootCmd missing %v: cmd=%v err=%v", path, cmd, err)
		}
	}
}

func TestBuildMonitorDefinition(t *testing.T) {
	def, err := buildMonitorDefinition("launch", "", "https://Example.com/docs?q=1", "")
	if err != nil {
		t.Fatalf("build url monitor: %v", err)
	}
	if def.Kind != "url" || def.Query != `url:"example.com/docs"` || def.SourceURL != "example.com/docs" {
		t.Fatalf("url monitor = %+v", def)
	}
	def, err = buildMonitorDefinition("founder", "", "", "@sama")
	if err != nil {
		t.Fatalf("build account monitor: %v", err)
	}
	if def.Query != "from:sama" || def.Account != "sama" {
		t.Fatalf("account monitor = %+v", def)
	}
	if _, err := buildMonitorDefinition("bad", "ai", "example.com", ""); err == nil {
		t.Fatal("buildMonitorDefinition accepted multiple monitor sources")
	}
}

func TestMonitorResultsDedupeAndBriefItems(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-twitter.db")
	db, err := store.OpenWithContext(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	cmd := testCommand()
	def := monitorDefinition{Name: "launch", Kind: "query", Query: "launch"}
	if err := upsertMonitorDefinition(cmd, db, def); err != nil {
		t.Fatalf("upsert monitor: %v", err)
	}
	rec := &resolvedPostRecord{TweetID: "12345", URL: "https://x.com/i/web/status/12345", Text: "launch day", Source: "live", PublicMetrics: map[string]any{"like_count": float64(7)}}
	added, err := saveMonitorResult(cmd, db, "launch", rec, "2026-01-01T00:00:00Z")
	if err != nil || !added {
		t.Fatalf("first save added=%v err=%v", added, err)
	}
	added, err = saveMonitorResult(cmd, db, "launch", rec, "2026-01-01T00:01:00Z")
	if err != nil || added {
		t.Fatalf("duplicate save added=%v err=%v", added, err)
	}
	items, err := listMonitorResultItems(cmd, db, "launch", "", 10)
	if err != nil {
		t.Fatalf("list monitor results: %v", err)
	}
	if len(items) != 1 || items[0].TweetID != "12345" || items[0].Text != "launch day" {
		t.Fatalf("items = %+v", items)
	}
	brief := buildBrief("local", "24h", items)
	if brief.ItemCount != 1 || len(brief.Highlights) != 1 || !strings.Contains(brief.Highlights[0].Reason, "Recent source item") {
		t.Fatalf("brief = %+v", brief)
	}
}

func TestNormalizeAccountProfile(t *testing.T) {
	profile, err := normalizeAccountProfile(json.RawMessage(`{
		"id":"42",
		"username":"alice",
		"name":"Alice",
		"description":"builds things",
		"public_metrics":{"followers_count":10},
		"pinned_tweet_id":"999"
	}`), "local", "synced", false)
	if err != nil {
		t.Fatalf("normalizeAccountProfile: %v", err)
	}
	if profile.ProfileURL != "https://x.com/alice" || profile.PinnedTweetID != "999" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestPerformanceSnapshotsAnalyzeAndGrouping(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-twitter.db")
	db, err := store.OpenWithContext(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	cmd := testCommand()
	records := []*resolvedPostRecord{
		{TweetID: "11111", URL: "https://x.com/i/web/status/11111", CreatedAt: "2026-01-01T00:00:00Z", PostType: "original", PublicMetrics: map[string]any{"like_count": float64(4)}, Source: "local"},
		{TweetID: "22222", URL: "https://x.com/i/web/status/22222", CreatedAt: "2026-01-01T01:00:00Z", PostType: "original", PublicMetrics: map[string]any{"like_count": float64(8)}, Source: "local"},
	}
	snapshots, err := savePerformanceSnapshots(cmd, db, records, "24h")
	if err != nil {
		t.Fatalf("save snapshots: %v", err)
	}
	if len(snapshots) != 2 || snapshots[0].Metrics["like_count"].(float64) != 4 {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	groups, err := analyzePerformanceSnapshots(cmd, db, "", "type,label")
	if err != nil {
		t.Fatalf("analyze snapshots: %v", err)
	}
	if len(groups) != 1 || groups[0].Count != 2 || groups[0].Averages["like_count"] != 6 {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestTimelineAndBriefMarkdownWriters(t *testing.T) {
	item := collectionItemSnapshot{TweetID: "12345", URL: "https://x.com/i/web/status/12345", Text: "hello"}
	var timeline bytes.Buffer
	if err := writeTimelineExport(&timeline, timelineExportResult{Subject: "@alice", Source: "local", GeneratedAt: "2026-01-01T00:00:00Z", Items: []collectionItemSnapshot{item}}, "markdown"); err != nil {
		t.Fatalf("timeline markdown: %v", err)
	}
	if !strings.Contains(timeline.String(), "X timeline export") || !strings.Contains(timeline.String(), "hello") {
		t.Fatalf("timeline markdown = %s", timeline.String())
	}
	var brief bytes.Buffer
	if err := writeBriefMarkdown(&brief, buildBrief("local", "24h", []collectionItemSnapshot{item})); err != nil {
		t.Fatalf("brief markdown: %v", err)
	}
	if !strings.Contains(brief.String(), "Highlights") || !strings.Contains(brief.String(), "Sources") {
		t.Fatalf("brief markdown = %s", brief.String())
	}
}

func testCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd
}
