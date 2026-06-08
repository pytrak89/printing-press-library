// Copyright 2026 Cathryn Lavery and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type timelineExportResult struct {
	Source      string                   `json:"source"`
	Subject     string                   `json:"subject,omitempty"`
	Query       string                   `json:"query,omitempty"`
	Since       string                   `json:"since,omitempty"`
	GeneratedAt string                   `json:"generated_at"`
	Items       []collectionItemSnapshot `json:"items"`
}

func newNovelTimelineCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "timeline",
		Short:       "Timeline export workflows",
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE:        parentNoSubcommandRunE(flags),
	}
	cmd.AddCommand(newNovelTimelineExportCmd(flags))
	return cmd
}

func newNovelTimelineExportCmd(flags *rootFlags) *cobra.Command {
	var dbPath, query, since, format, output string
	var limit int
	var live bool
	cmd := &cobra.Command{
		Use:   "export [username-or-id]",
		Short: "Export an account or query timeline as markdown, JSON, or JSONL",
		Example: `  x-twitter-pp-cli timeline export @username --since 30d --format markdown
  x-twitter-pp-cli timeline export --query 'ai agents' --since 7d --format jsonl`,
		Annotations: map[string]string{"mcp:read-only": "true", "pp:no-error-path-probe": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			mode := flags.dataSource
			if live {
				mode = "live"
			}
			result, err := buildTimelineExport(cmd, flags, dbPath, mode, args, query, since, limit)
			if err != nil {
				return err
			}
			var w io.Writer = cmd.OutOrStdout()
			var f *os.File
			if output != "" {
				f, err = os.Create(output)
				if err != nil {
					return fmt.Errorf("creating export file: %w", err)
				}
				defer f.Close()
				w = f
			}
			if err := writeTimelineExport(w, result, format); err != nil {
				return err
			}
			if output != "" && (flags.asJSON || flags.agent) {
				return printJSONFiltered(cmd.OutOrStdout(), map[string]any{"export_path": output, "items": len(result.Items)}, flags)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to the local database")
	cmd.Flags().StringVar(&query, "query", "", "Recent-search query to export")
	cmd.Flags().StringVar(&since, "since", "30d", "Timeline window such as 24h, 7d, RFC3339, or YYYY-MM-DD")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum posts to export")
	cmd.Flags().StringVar(&format, "format", "markdown", "Export format: markdown, json, or jsonl")
	cmd.Flags().StringVar(&output, "output", "", "Write export to a file instead of stdout")
	cmd.Flags().BoolVar(&live, "live", false, "Bypass local account timeline lookup")
	return cmd
}

func buildTimelineExport(cmd *cobra.Command, flags *rootFlags, dbPath, mode string, args []string, query, since string, limit int) (timelineExportResult, error) {
	if limit <= 0 {
		limit = 100
	}
	result := timelineExportResult{Since: since, GeneratedAt: generatedAt()}
	if strings.TrimSpace(query) != "" {
		result.Query = query
		if mode == "local" {
			records, err := localTimelineQuery(cmd, dbPath, query, limit)
			if err != nil {
				return result, err
			}
			result.Source = "local"
			for _, rec := range records {
				result.Items = append(result.Items, collectionItemFromPost(rec, ""))
			}
			return result, nil
		}
		records, err := recentSearchRecords(cmd, flags, query, limit, since, "", parseIncludeSet("author,links,refs,metrics"))
		if err != nil {
			return result, classifyAPIError(err, flags)
		}
		result.Source = "live"
		for _, rec := range records {
			result.Items = append(result.Items, collectionItemFromPost(rec, ""))
		}
		return result, nil
	}
	if len(args) == 0 {
		return result, usageErr(fmt.Errorf("username/user ID required unless --query is set"))
	}
	profile, err := resolveAccountProfile(cmd, flags, args[0], dbPath, mode, false)
	if err != nil {
		return result, err
	}
	result.Subject = profile.ProfileURL
	if dbPath == "" {
		dbPath = defaultDBPath("x-twitter-pp-cli")
	}
	var records []*resolvedPostRecord
	if mode != "live" {
		records, _ = localRecentPostsForAccount(cmd, dbPath, profile.ID, limit, parseIncludeSet("author,links,refs,metrics"))
	}
	if len(records) == 0 && mode != "local" {
		records, err = liveRecentPostsForAccount(cmd, flags, profile.ID, limit, parseIncludeSet("author,links,refs,metrics"))
		if err != nil {
			return result, classifyAPIError(err, flags)
		}
		result.Source = "live"
	} else {
		result.Source = "local"
	}
	for _, rec := range records {
		result.Items = append(result.Items, collectionItemFromPost(rec, ""))
	}
	return result, nil
}

func localTimelineQuery(cmd *cobra.Command, dbPath, query string, limit int) ([]*resolvedPostRecord, error) {
	db, err := openWorkflowDB(cmd, dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.DB().QueryContext(cmd.Context(),
		`SELECT data FROM tweets WHERE lower(text) LIKE '%' || lower(?) || '%' ORDER BY created_at DESC LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*resolvedPostRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		rec, err := normalizeTweetRecordWithOwnID(json.RawMessage(raw), nil, "local", "synced", parseIncludeSet("author,links,refs,metrics"))
		if err == nil {
			out = append(out, rec)
		}
	}
	return out, rows.Err()
}

func writeTimelineExport(w io.Writer, result timelineExportResult, format string) error {
	switch strings.ToLower(format) {
	case "markdown", "md":
		title := result.Subject
		if title == "" {
			title = result.Query
		}
		fmt.Fprintf(w, "# X timeline export: %s\n\n", title)
		fmt.Fprintf(w, "- Generated: %s\n- Source: %s\n- Items: %d\n\n", result.GeneratedAt, result.Source, len(result.Items))
		for _, item := range result.Items {
			fmt.Fprintf(w, "## %s\n\n", item.URL)
			if item.Author != nil {
				fmt.Fprintf(w, "- Author: %s\n", authorDisplay(item.Author))
			}
			fmt.Fprintf(w, "\n%s\n\n", item.Text)
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, item := range result.Items {
			if err := enc.Encode(item); err != nil {
				return err
			}
		}
		return nil
	default:
		return usageErr(fmt.Errorf("invalid --format %q: expected markdown, json, or jsonl", format))
	}
}
