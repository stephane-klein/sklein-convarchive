package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/stephane-klein/sklein-convarchive/pkg/archive"
	"github.com/stephane-klein/sklein-convarchive/pkg/chatgpt"
	"github.com/stephane-klein/sklein-convarchive/pkg/claude"
	"github.com/stephane-klein/sklein-convarchive/pkg/ui"
)

// backupExtensionName is the Firefox extension that produces the exported
// conversation files this command imports.
const backupExtensionName = "claude-chatgpt-backup-extension"

// backupExtensionRepo is the repository of the backup extension.
const backupExtensionRepo = "https://github.com/stephane-klein/claude-chatgpt-backup-extension"

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.AddCommand(exportArchiveCmd)

	exportArchiveCmd.Flags().StringSlice("file", nil, "Path(s) to the Claude.ai or ChatGPT export JSON file(s) (required, repeatable)")
	exportArchiveCmd.Flags().String("source", "auto", "Source format: claude, chatgpt, or auto (detect from the file content)")
	exportArchiveCmd.Flags().String("account", "", "Account/workspace label used in the object path (default: derived from the filename)")
	exportArchiveCmd.Flags().String("period", "", "Period to archive: YYYY-MM (month) or YYYY (year)")
}

var exportCmd = &cobra.Command{
	Use:   "claude-chatgpt-backup-extension-export",
	Short: "Import Claude.ai and ChatGPT conversation exports",
	Long: `Imports conversation export files produced by the "` + backupExtensionName + `"
Firefox extension (` + backupExtensionRepo + `) into Object Storage, one object
per conversation (thread), named after the thread creation datetime.

The source format is auto-detected from the file content (Claude.ai
"chat_messages" layout or ChatGPT "mapping" layout); use --source to force one.`,
}

var exportArchiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Import a Claude.ai or ChatGPT export file into Object Storage",
	Long: `Archives every conversation (thread) of a JSON export file produced by the
"` + backupExtensionName + `" Firefox extension (` + backupExtensionRepo + `),
normalized to the common JSONL schema and rendered as Markdown, into Object
Storage.

Each thread becomes one JSONL object and one Markdown object named after the
thread creation datetime (yyyy-mm-dd_hhmmss). This command is always used
explicitly, with --file repeated for each file: its options are not
configurable via a configuration file or environment variables.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runExportArchive(cmd)
	},
}

func runExportArchive(cmd *cobra.Command) {
	// NotifyContext returns a context canceled on Ctrl+C or SIGTERM, so
	// long-running HTTP calls can abort cleanly instead of hanging forever.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	files, _ := cmd.Flags().GetStringSlice("file")
	sourceFlag, _ := cmd.Flags().GetString("source")
	accountFlag, _ := cmd.Flags().GetString("account")
	periodFlag, _ := cmd.Flags().GetString("period")

	if len(files) == 0 {
		printError("--file is required")
		os.Exit(1)
	}

	loc := getTimezone()
	owner := viper.GetString("owner")

	startMs, endMs := int64(0), int64(0)
	var err error
	if periodFlag != "" {
		startMs, endMs, err = parsePeriod(periodFlag, loc)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}
	}

	encryptor, err := getEncryptor()
	if err != nil {
		printError("%v", err)
		os.Exit(1)
	}

	var uploader archive.ObjectPutter
	if !isDryRun() {
		s3 := getS3Config()
		uploader, err = archive.NewUploader(ctx, s3.Endpoint, s3.AccessKey, s3.SecretKey, s3.Bucket, s3.UseSSL)
		if err != nil {
			printError("failed to connect to object storage: %v", err)
			os.Exit(1)
		}
		uploader, err = archive.NewChainedUploader(uploader, isCompress(), encryptor, nil)
		if err != nil {
			printError("failed to build uploader chain: %v", err)
			os.Exit(1)
		}
	}

	d := ui.New(os.Stderr)

	var wouldUpload []string
	var failedFiles []string
	interrupted := false

	for _, file := range files {
		if ctx.Err() != nil {
			interrupted = true
			break
		}

		data, readErr := os.ReadFile(file)
		if readErr != nil {
			printError("failed to read export file %q: %v", file, readErr)
			failedFiles = append(failedFiles, file)
			continue
		}

		source := sourceFlag
		if source == "auto" {
			source, err = detectExportSource(data)
			if err != nil {
				printError("%v", err)
				failedFiles = append(failedFiles, file)
				continue
			}
		}

		account := accountFlag
		if account == "" {
			account = accountFromFilename(file)
		}

		fmt.Fprintf(os.Stderr, "Source:  %s\n", source)
		fmt.Fprintf(os.Stderr, "Account: %s\n", account)

		root := d.Root("Import " + filepath.Base(file))
		root.Status = ui.StatusRunning
		root.StatusText = "… parsing …"
		d.Start()

		var threads []*archive.Thread
		switch source {
		case claude.Source:
			threads, err = claude.Parse(bytes.NewReader(data), claude.ExportOptions{Owner: owner, Loc: loc})
		case chatgpt.Source:
			threads, err = chatgpt.Parse(bytes.NewReader(data), chatgpt.ExportOptions{Owner: owner, Loc: loc})
		default:
			printError("invalid --source %q: expected claude, chatgpt, or auto", source)
			os.Exit(1)
		}
		if err != nil {
			d.Update(func() {
				root.Status = ui.StatusError
				root.StatusText = "parse failed"
			})
			printError("failed to parse export file %q: %v", file, err)
			failedFiles = append(failedFiles, file)
			continue
		}

		threadTasks := make([]*ui.Task, 0, len(threads))
		d.Update(func() {
			root.StatusText = ""
			root.MaxVisibleChildren = 15
			root.AnchorFirstWhenPending = true
			root.CollapseWhenInactive = true
			root.HiddenChildrenLabel = "thread"
			root.CollapsedSummary = fmt.Sprintf("· %d threads", len(threads))
			for _, thread := range threads {
				threadTasks = append(threadTasks, root.AddChild(threadTaskTitle(thread, loc)))
			}
		})

		fileFailed := false
		fail := func(task *ui.Task, fileErr error) {
			d.Update(func() {
				task.Status = ui.StatusError
				if ctx.Err() != nil {
					task.StatusText = "interrupted"
				} else {
					task.StatusText = "failed"
				}
			})
			if ctx.Err() != nil {
				interrupted = true
				return
			}
			printError("%v", fileErr)
			if !fileFailed {
				fileFailed = true
				failedFiles = append(failedFiles, file)
			}
		}

		fileUploaded, fileSkipped := 0, 0
		for i, thread := range threads {
			if ctx.Err() != nil {
				interrupted = true
				break
			}

			task := threadTasks[i]
			d.Update(func() {
				task.Status = ui.StatusRunning
				task.StatusText = "… rendering …"
			})

			entries := thread.Entries
			if startMs != 0 || endMs != 0 {
				entries = filterEntriesByPeriod(entries, startMs, endMs)
			}
			if len(entries) == 0 {
				d.Update(func() {
					task.Status = ui.StatusSuccess
					task.StatusText = "skipped"
				})
				fileSkipped++
				continue
			}

			created := thread.CreatedAt.In(loc)
			year := created.Year()
			datetime := created.Format("2006-01-02_150405")

			jsonlKey, err := archive.JSONLThreadObjectKey(thread.Source, account, year, datetime)
			if err != nil {
				fail(task, err)
				continue
			}
			mdKey, err := archive.MarkdownThreadObjectKey(thread.Source, account, year, datetime)
			if err != nil {
				fail(task, err)
				continue
			}

			jsonlContent, err := archive.MarshalThreadJSONL(entries)
			if err != nil {
				fail(task, err)
				continue
			}
			mdContent, err := archive.RenderThreadMarkdown(entries, archive.ConversationMeta{
				Source:      thread.Source,
				TeamName:    account,
				DisplayName: thread.DisplayName,
			})
			if err != nil {
				fail(task, err)
				continue
			}

			if isDryRun() {
				wouldUpload = append(wouldUpload,
					fmt.Sprintf("  would upload %s (%d lines)", displayObjectKey(jsonlKey, encryptor != nil), len(entries)),
					fmt.Sprintf("  would upload %s (%d posts)", displayObjectKey(mdKey, encryptor != nil), len(entries)),
				)
				d.Update(func() {
					task.Status = ui.StatusSuccess
					task.StatusText = fmt.Sprintf("Ok · %d messages", len(entries))
				})
				continue
			}

			if err := uploader.Put(ctx, jsonlKey, jsonlContent, "application/x-ndjson"); err != nil {
				fail(task, err)
				continue
			}
			if err := uploader.Put(ctx, mdKey, mdContent, "text/markdown"); err != nil {
				fail(task, err)
				continue
			}
			fileUploaded++
			d.Update(func() {
				task.Status = ui.StatusSuccess
				task.StatusText = fmt.Sprintf("Ok · %d messages", len(entries))
			})
		}
		d.Update(func() {
			if interrupted {
				root.Status = ui.StatusError
				root.StatusText = "interrupted"
				return
			}
			root.Status = ui.StatusSuccess
			if isDryRun() {
				root.StatusText = fmt.Sprintf("Dry run · %d thread(s) would upload, %d skipped", len(threads)-fileSkipped, fileSkipped)
			} else {
				root.StatusText = fmt.Sprintf("Ok · %d thread(s) uploaded, %d skipped", fileUploaded, fileSkipped)
			}
		})
	}

	d.Stop()

	if interrupted {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Interrupted, import stopped")
		os.Exit(130)
	}

	if len(failedFiles) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Error: %d file(s) failed:\n", len(failedFiles))
		for _, file := range failedFiles {
			fmt.Fprintf(os.Stderr, "  - %s\n", file)
		}
		os.Exit(1)
	}

	if isDryRun() {
		fmt.Fprintln(os.Stderr)
		for _, line := range wouldUpload {
			fmt.Fprintln(os.Stderr, line)
		}
		fmt.Fprintln(os.Stderr, "Dry run: no upload performed")
		return
	}
}

// detectExportSource identifies the export format from the shape of the first
// element of the top-level array.
func detectExportSource(data []byte) (string, error) {
	var first []map[string]json.RawMessage
	if err := json.Unmarshal(data, &first); err != nil {
		return "", fmt.Errorf("invalid export file: not a JSON array: %w", err)
	}
	if len(first) == 0 {
		return "", fmt.Errorf("invalid export file: empty array")
	}
	if _, ok := first[0]["chat_messages"]; ok {
		return claude.Source, nil
	}
	if _, ok := first[0]["mapping"]; ok {
		return chatgpt.Source, nil
	}
	return "", fmt.Errorf("unable to detect export format: first object has neither \"chat_messages\" (Claude) nor \"mapping\" (ChatGPT)")
}

// accountFromFilename derives the account label from the export filename, e.g.
// "2024-03-14T1015+1_claude_all_conversations_account_demo.json" gives
// "account_demo". Falls back to "default".
func accountFromFilename(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	for _, marker := range []string{"_claude_all_conversations", "_chatgpt_all_conversations"} {
		if i := strings.Index(name, marker); i >= 0 {
			suffix := strings.TrimPrefix(name[i+len(marker):], "_")
			if suffix == "" {
				return "default"
			}
			return suffix
		}
	}
	return "default"
}

// filterEntriesByPeriod keeps the entries whose timestamp falls inside the
// [startMs, endMs] range.
func filterEntriesByPeriod(entries []*archive.Entry, startMs, endMs int64) []*archive.Entry {
	filtered := make([]*archive.Entry, 0, len(entries))
	for _, e := range entries {
		ts, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			continue
		}
		ms := ts.UnixMilli()
		if ms >= startMs && ms <= endMs {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// displayObjectKey renders the object key with the compression and encryption
// suffixes that the uploader chain would append.
func displayObjectKey(key string, encrypted bool) string {
	if isCompress() {
		key += ".zst"
	}
	if encrypted {
		key += ".age"
	}
	return key
}

// threadTaskTitle renders the task title of a thread in the progress tree:
// its creation datetime followed by the conversation name, truncated to keep
// the tree lines readable.
func threadTaskTitle(thread *archive.Thread, loc *time.Location) string {
	created := thread.CreatedAt.In(loc).Format("2006-01-02_150405")
	title := created + " " + thread.DisplayName
	if utf8.RuneCountInString(title) > 60 {
		r := []rune(title)
		title = string(r[:60]) + "…"
	}
	return title
}
