package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/taskscli/config"
	"github.com/hollis-labs/tesseract/internal/taskscli/index"
	"github.com/hollis-labs/tesseract/internal/taskscli/taskfile"
)

const dateFormat = "2006-01-02"

// Runner executes task subcommands.
type Runner struct {
	cfg    *config.Config
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
}

// Run is the entry point for `volon task`.
func Run(repoRoot string, args []string) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return err
	}
	r := newRunner(cfg)
	return r.Dispatch(args)
}

func newRunner(cfg *config.Config) *Runner {
	return &Runner{
		cfg:    cfg,
		stdout: os.Stdout,
		stderr: os.Stderr,
		now:    time.Now,
	}
}

// Dispatch selects and executes a subcommand.
func (r *Runner) Dispatch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon task <subcommand> [flags]")
	}

	switch args[0] {
	case "create":
		return r.cmdCreate(args[1:])
	case "start":
		return r.cmdStart(args[1:])
	case "done":
		return r.cmdDone(args[1:])
	case "show":
		return r.cmdShow(args[1:])
	case "list":
		return r.cmdList(args[1:])
	case "reindex":
		return r.cmdReindex()
	default:
		return fmt.Errorf("unknown task subcommand %q", args[0])
	}
}

func (r *Runner) cmdCreate(args []string) error {
	fs := flag.NewFlagSet("volon task create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	taskType := fs.String("type", "", "Task type (feature|bug|review|design|verify)")
	priority := fs.String("priority", "B", "Priority (A|B|C)")
	tagsFlag := csvList{}
	fs.Var(&tagsFlag, "tags", "Comma-separated tags")
	parent := fs.String("parent", "", "Parent task ID")
	sprint := fs.String("sprint", "", "Sprint identifier (e.g. sprint-2026-02)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("title is required")
	}

	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if title == "" {
		return errors.New("title cannot be empty")
	}

	now := r.now().UTC()
	id, err := r.nextTaskID(now)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(r.cfg.TasksDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(r.cfg.TasksDir, id+".md")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("task file %s already exists", path)
	}

	tf := taskfile.New(path)
	tf.SetString("id", id)
	tf.SetString("title", title)
	tf.SetString("status", "todo")
	tf.SetString("priority", strings.ToUpper(*priority))
	tf.SetString("project", r.cfg.ProjectName)
	tf.SetString("context", r.cfg.DefaultContext)
	tf.SetString("created_at", now.Format(dateFormat))
	tf.SetString("updated_at", now.Format(dateFormat))
	if *taskType != "" {
		tf.SetString("type", *taskType)
	}
	if *parent != "" {
		tf.SetString("parent_id", *parent)
	}
	if *sprint != "" {
		tf.SetString("sprint_id", *sprint)
	}
	if len(tagsFlag) > 0 {
		tf.SetTags([]string(tagsFlag))
	} else {
		tf.SetTags([]string{})
	}

	if err := tf.Save(); err != nil {
		return err
	}

	r.updateIndexFromFile(tf)

	fmt.Fprintf(r.stdout, "Created %s at ./%s\n", id, r.relative(path))
	return nil
}

func (r *Runner) cmdStart(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon task start <id>")
	}
	return r.transitionTask(strings.TrimSpace(args[0]), "todo", "doing", "started")
}

func (r *Runner) cmdDone(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon task done <id>")
	}
	return r.transitionTask(strings.TrimSpace(args[0]), "doing", "done", "completed")
}

func (r *Runner) cmdShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon task show <id>")
	}
	path := filepath.Join(r.cfg.TasksDir, args[0]+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = r.stdout.Write(data)
	return err
}

func (r *Runner) cmdList(args []string) error {
	fs := flag.NewFlagSet("volon task list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var statuses, types, tags, priorities, sprints csvList
	fs.Var(&statuses, "status", "Status filter (repeatable)")
	fs.Var(&types, "type", "Type filter (repeatable)")
	fs.Var(&tags, "tag", "Tag substring filter (repeatable)")
	fs.Var(&priorities, "priority", "Priority filter (repeatable)")
	fs.Var(&sprints, "sprint", "Sprint filter (repeatable)")
	limit := fs.Int("limit", 0, "Max results")
	if err := fs.Parse(args); err != nil {
		return err
	}

	filter := index.Filters{
		Statuses:  statuses,
		Types:     types,
		Tags:      tags,
		Priority:  priorities,
		SprintIDs: sprints,
		Limit:     *limit,
	}

	ctx := context.Background()
	rows, err := r.listFromIndex(ctx, filter)
	if err != nil {
		fmt.Fprintf(r.stderr, "SQLite unavailable (%v); falling back to file scan\n", err)
		rows, err = r.listFromFiles(filter)
		if err != nil {
			return err
		}
	}

	r.printRows(rows)
	return nil
}

func (r *Runner) cmdReindex() error {
	ctx := context.Background()
	if err := r.reindex(ctx); err != nil {
		return err
	}
	fmt.Fprintln(r.stdout, "SQLite index rebuilt.")
	return nil
}

func (r *Runner) transitionTask(id, fromStatus, toStatus, verb string) error {
	path := filepath.Join(r.cfg.TasksDir, id+".md")
	tf, err := taskfile.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("task %s not found (expected %s)", id, r.relative(path))
		}
		return err
	}
	meta := tf.Metadata()
	if meta.Status != fromStatus {
		return fmt.Errorf("task %s is %s (expected %s)", id, meta.Status, fromStatus)
	}

	now := r.now().UTC()
	tf.SetString("status", toStatus)
	tf.SetString("updated_at", now.Format(dateFormat))
	update := fmt.Sprintf("[%s] status → %s (%s)", now.Format(dateFormat), toStatus, verb)
	if err := tf.AppendUpdate(update); err != nil {
		return err
	}
	if err := tf.Save(); err != nil {
		return err
	}

	r.updateIndexFromFile(tf)
	return nil
}

func (r *Runner) updateIndexFromFile(tf *taskfile.File) {
	ctx := context.Background()
	store, err := index.Open(ctx, r.cfg.DBPath)
	if err != nil {
		r.warn("SQLite unavailable (%v); run `volon task reindex` once resolved", err)
		return
	}
	defer store.Close()

	meta := tf.Metadata()
	if err := store.UpsertTask(ctx, r.metaToRow(meta)); err != nil {
		r.warn("Failed to update SQLite index for %s: %v", meta.ID, err)
	}
}

func (r *Runner) nextTaskID(now time.Time) (string, error) {
	date := now.Format("20060102")
	prefix := fmt.Sprintf("TASK-%s-", date)
	pattern := filepath.Join(r.cfg.TasksDir, prefix+"*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	max := 0
	for _, path := range matches {
		base := filepath.Base(path)
		numPart := strings.TrimSuffix(strings.TrimPrefix(base, prefix), ".md")
		n, err := strconv.Atoi(numPart)
		if err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("TASK-%s-%03d", date, max+1), nil
}

func (r *Runner) listFromIndex(ctx context.Context, filter index.Filters) ([]index.TaskRow, error) {
	store, err := index.Open(ctx, r.cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListTasks(ctx, filter)
}

func (r *Runner) reindex(ctx context.Context) error {
	store, err := index.Open(ctx, r.cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	rows, err := r.readAllRows()
	if err != nil {
		return err
	}
	return store.ReplaceAll(ctx, rows)
}

func (r *Runner) listFromFiles(filter index.Filters) ([]index.TaskRow, error) {
	rows, err := r.readAllRows()
	if err != nil {
		return nil, err
	}
	filtered := rows[:0]
	for _, row := range rows {
		if matchesFilters(row, filter) {
			filtered = append(filtered, row)
		}
	}
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}
	return filtered, nil
}

func (r *Runner) readAllRows() ([]index.TaskRow, error) {
	pattern := filepath.Join(r.cfg.TasksDir, "TASK-*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	var rows []index.TaskRow
	for _, path := range matches {
		tf, err := taskfile.Load(path)
		if err != nil {
			fmt.Fprintf(r.stderr, "skip %s: %v\n", path, err)
			continue
		}
		rows = append(rows, r.metaToRow(tf.Metadata()))
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})

	return rows, nil
}

func (r *Runner) metaToRow(meta taskfile.Metadata) index.TaskRow {
	return index.TaskRow{
		ID:          meta.ID,
		Title:       meta.Title,
		Status:      meta.Status,
		Priority:    meta.Priority,
		Type:        meta.Type,
		Tags:        strings.Join(meta.Tags, ","),
		Project:     meta.Project,
		Context:     meta.Context,
		ParentID:    meta.ParentID,
		SprintID:    meta.SprintID,
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   meta.UpdatedAt,
		IterationID: meta.IterationID,
		FilePath:    r.relative(meta.FilePath),
	}
}

func (r *Runner) relative(path string) string {
	rel, err := filepath.Rel(r.cfg.RepoPath, path)
	if err != nil {
		return path
	}
	return rel
}

func (r *Runner) warn(format string, args ...interface{}) {
	fmt.Fprintf(r.stderr, "[volon task] %s\n", fmt.Sprintf(format, args...))
}

func matchesFilters(row index.TaskRow, filter index.Filters) bool {
	if len(filter.Statuses) > 0 && !contains(filter.Statuses, row.Status) {
		return false
	}
	if len(filter.Types) > 0 && !contains(filter.Types, row.Type) {
		return false
	}
	if len(filter.Priority) > 0 && !contains(filter.Priority, row.Priority) {
		return false
	}
	if len(filter.SprintIDs) > 0 && !contains(filter.SprintIDs, row.SprintID) {
		return false
	}
	if len(filter.Tags) > 0 {
		tagString := strings.ToLower(row.Tags)
		for _, tag := range filter.Tags {
			if !strings.Contains(tagString, strings.ToLower(tag)) {
				return false
			}
		}
	}
	return true
}

func contains(list []string, needle string) bool {
	for _, item := range list {
		if strings.EqualFold(item, needle) {
			return true
		}
	}
	return false
}

func (r *Runner) printRows(rows []index.TaskRow) {
	fmt.Fprintf(r.stdout, "%-16s %-6s %-3s %-10s %-10s %s\n", "ID", "Status", "Pri", "Sprint", "Updated", "Title")
	for _, row := range rows {
		updated := row.UpdatedAt
		if len(updated) > 10 {
			updated = updated[:10]
		}
		sprint := row.SprintID
		if sprint == "" {
			sprint = "-"
		}
		fmt.Fprintf(r.stdout, "%-16s %-6s %-3s %-10s %-10s %s\n",
			row.ID, row.Status, row.Priority, sprint, updated, row.Title)
	}
}

type csvList []string

func (c *csvList) String() string {
	return strings.Join(*c, ",")
}

func (c *csvList) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*c = append(*c, part)
		}
	}
	return nil
}

// WithStdout configures test output.
func (r *Runner) WithStdout(w io.Writer) {
	r.stdout = w
}

// WithStderr configures test error output.
func (r *Runner) WithStderr(w io.Writer) {
	r.stderr = w
}

// WithNow overrides the clock (for deterministic tests).
func (r *Runner) WithNow(fn func() time.Time) {
	r.now = fn
}

// CaptureOutput runs fn while capturing stdout/stderr (used in tests).
func CaptureOutput(fn func(stdout, stderr io.Writer)) (string, string) {
	var outBuf, errBuf bytes.Buffer
	fn(&outBuf, &errBuf)
	return outBuf.String(), errBuf.String()
}
