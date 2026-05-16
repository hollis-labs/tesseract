package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/taskscli/config"
	"github.com/hollis-labs/tesseract/internal/taskscli/taskfile"
)

// RunBacklog executes `volon backlog <subcommand>`.
func RunBacklog(repoRoot string, args []string) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return err
	}
	r := newRunner(cfg)
	return r.DispatchBacklog(args)
}

// DispatchBacklog runs backlog subcommands.
func (r *Runner) DispatchBacklog(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon backlog <subcommand> [flags]")
	}
	switch args[0] {
	case "list":
		return r.cmdBacklogList(args[1:])
	case "show":
		return r.cmdBacklogShow(args[1:])
	case "promote":
		return r.cmdBacklogPromote(args[1:])
	default:
		return fmt.Errorf("unknown backlog subcommand %q", args[0])
	}
}

type backlogItem struct {
	ID        string
	Title     string
	Status    string
	Priority  string
	Tags      []string
	UpdatedAt string
	Path      string
}

func (r *Runner) cmdBacklogList(args []string) error {
	fs := flag.NewFlagSet("volon backlog list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var statuses, priorities, tags csvList
	limit := fs.Int("limit", 0, "Max results")
	fs.Var(&statuses, "status", "Status filter (repeatable)")
	fs.Var(&priorities, "priority", "Priority filter (repeatable)")
	fs.Var(&tags, "tag", "Tag substring filter (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	items, err := r.loadBacklogItems()
	if err != nil {
		return err
	}
	filtered := filterBacklog(items, statuses, priorities, tags, *limit)
	fmt.Fprintf(r.stdout, "%-18s %-9s %-3s %-10s %s\n", "ID", "Status", "Pri", "Updated", "Title")
	for _, item := range filtered {
		updated := item.UpdatedAt
		if len(updated) > 10 {
			updated = updated[:10]
		}
		fmt.Fprintf(r.stdout, "%-18s %-9s %-3s %-10s %s\n",
			item.ID, item.Status, item.Priority, updated, item.Title)
	}
	return nil
}

func (r *Runner) cmdBacklogShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon backlog show <id>")
	}
	path := filepath.Join(r.cfg.BacklogDir, args[0]+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = r.stdout.Write(data)
	return err
}

func (r *Runner) cmdBacklogPromote(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: volon backlog promote <id> [flags]")
	}
	backlogID := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("volon backlog promote", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "Override task title")
	priority := fs.String("priority", "", "Task priority (A|B|C)")
	taskType := fs.String("type", "", "Task type (feature|bug|review|design|verify)")
	sprint := fs.String("sprint", "", "Sprint ID to record on the new task")
	var tags csvList
	fs.Var(&tags, "tags", "Comma-separated tags for the new task")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	item, err := r.loadBacklogFile(backlogID)
	if err != nil {
		return err
	}
	meta := item.Metadata()
	if strings.EqualFold(meta.Status, "promoted") {
		return fmt.Errorf("%s already promoted", backlogID)
	}
	if strings.EqualFold(meta.Status, "dropped") {
		return fmt.Errorf("%s is dropped; cannot promote", backlogID)
	}

	taskTitle := strings.TrimSpace(*title)
	if taskTitle == "" {
		taskTitle = meta.Title
	}
	if taskTitle == "" {
		return errors.New("task title missing — use --title to set one")
	}
	taskPriority := strings.ToUpper(*priority)
	if taskPriority == "" {
		if meta.Priority != "" {
			taskPriority = strings.ToUpper(meta.Priority)
		} else {
			taskPriority = "B"
		}
	}
	taskTags := []string(tags)
	if len(taskTags) == 0 {
		taskTags = meta.Tags
	}

	now := r.now().UTC()
	taskID, taskFile, err := r.buildTaskFromBacklog(now, taskTitle, taskPriority, taskTags, *taskType, *sprint, backlogID)
	if err != nil {
		return err
	}

	item.SetString("status", "promoted")
	item.SetString("promoted_to", taskID)
	item.SetString("updated_at", now.Format(dateFormat))

	if err := taskFile.Save(); err != nil {
		return err
	}
	if err := item.Save(); err != nil {
		os.Remove(taskFile.Path)
		return err
	}
	r.updateIndexFromFile(taskFile)

	fmt.Fprintf(r.stdout, "Promoted %s -> %s at ./%s\n", backlogID, taskID, r.relative(taskFile.Path))
	return nil
}

func (r *Runner) loadBacklogItems() ([]backlogItem, error) {
	pattern := filepath.Join(r.cfg.BacklogDir, "BACKLOG-*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	items := make([]backlogItem, 0, len(matches))
	for _, path := range matches {
		info, err := taskfile.Load(path)
		if err != nil {
			return nil, err
		}
		meta := info.Metadata()
		items = append(items, backlogItem{
			ID:        meta.ID,
			Title:     meta.Title,
			Status:    meta.Status,
			Priority:  meta.Priority,
			Tags:      meta.Tags,
			UpdatedAt: meta.UpdatedAt,
			Path:      path,
		})
	}
	return items, nil
}

func (r *Runner) loadBacklogFile(id string) (*taskfile.File, error) {
	if id == "" {
		return nil, errors.New("backlog ID is required")
	}
	path := filepath.Join(r.cfg.BacklogDir, id+".md")
	return taskfile.Load(path)
}

func filterBacklog(items []backlogItem, statuses, priorities, tags []string, limit int) []backlogItem {
	var out []backlogItem
	for _, item := range items {
		if len(statuses) > 0 && !containsFold(statuses, item.Status) {
			continue
		}
		if len(priorities) > 0 && !containsFold(priorities, item.Priority) {
			continue
		}
		if len(tags) > 0 && !matchTags(tags, item.Tags) {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func containsFold(list []string, value string) bool {
	value = strings.ToLower(value)
	for _, item := range list {
		if strings.ToLower(item) == value {
			return true
		}
	}
	return false
}

func matchTags(filters []string, tags []string) bool {
outer:
	for _, filter := range filters {
		filter = strings.ToLower(filter)
		for _, tag := range tags {
			if strings.Contains(strings.ToLower(tag), filter) {
				continue outer
			}
		}
		return false
	}
	return true
}

func (r *Runner) buildTaskFromBacklog(now time.Time, title, priority string, tags []string, taskType, sprint, backlogID string) (string, *taskfile.File, error) {
	id, err := r.nextTaskID(now)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(r.cfg.TasksDir, 0o755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(r.cfg.TasksDir, id+".md")
	tf := taskfile.New(path)
	tf.SetString("id", id)
	tf.SetString("title", title)
	tf.SetString("status", "todo")
	tf.SetString("priority", strings.ToUpper(priority))
	tf.SetString("project", r.cfg.ProjectName)
	tf.SetString("context", r.cfg.DefaultContext)
	tf.SetString("created_at", now.Format(dateFormat))
	tf.SetString("updated_at", now.Format(dateFormat))
	tf.SetString("promoted_from", backlogID)
	if taskType != "" {
		tf.SetString("type", taskType)
	}
	if sprint != "" {
		tf.SetString("sprint_id", sprint)
	}
	if len(tags) > 0 {
		tf.SetTags(tags)
	} else {
		tf.SetTags([]string{})
	}
	return id, tf, nil
}
