package report

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Render produces the Markdown document for a report. The output is
// deterministic: projects are sorted by path, commits by push time, MRs and
// issues by IID.
func Render(rep *Report) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Daily report — %s\n\n", rep.Date)
	writeIdentity(&b, rep)

	if !rep.Summary.HasActivity() {
		b.WriteString("## No activity\n\n")
		fmt.Fprintf(&b, "_No activity recorded for %s._\n", rep.Date)
		return finish(&b)
	}

	writeSummary(&b, rep.Summary)
	writeGlance(&b, rep)
	for _, project := range rep.Projects {
		writeProject(&b, rep, project)
	}
	return finish(&b)
}

// finish trims trailing blank lines so every report ends with exactly one
// newline.
func finish(b *bytes.Buffer) []byte {
	out := bytes.TrimRight(b.Bytes(), "\n")
	out = append(out, '\n')
	return out
}

// WriteFile renders the report and writes reports/YYYY-MM-DD.md. The file is
// mode 0600 because commit messages and MR titles are internal data.
func WriteFile(dir string, rep *Report) (string, error) {
	if dir == "" {
		dir = "./reports"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create report directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, rep.Date+".md")
	if err := os.WriteFile(path, Render(rep), 0o600); err != nil {
		return "", fmt.Errorf("write report %s: %w", path, err)
	}
	return path, nil
}

func writeIdentity(b *bytes.Buffer, rep *Report) {
	name := strings.TrimSpace(rep.User.Name)
	username := strings.TrimSpace(rep.User.Username)
	switch {
	case name != "" && username != "":
		fmt.Fprintf(b, "**%s** (@%s) · %s\n\n", name, username, rep.Timezone)
	case username != "":
		fmt.Fprintf(b, "@%s · %s\n\n", username, rep.Timezone)
	default:
		fmt.Fprintf(b, "%s\n\n", rep.Timezone)
	}
}

func writeSummary(b *bytes.Buffer, s Summary) {
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(b, "- %d %s\n", s.Projects, pluralize(s.Projects, "project", "projects"))
	if s.Pushes > 0 {
		fmt.Fprintf(b, "- %d %s\n", s.Pushes, pluralize(s.Pushes, "push", "pushes"))
	}
	if s.Commits > 0 {
		fmt.Fprintf(b, "- %d %s\n", s.Commits, pluralize(s.Commits, "commit", "commits"))
	}
	if s.Lines() > 0 {
		fmt.Fprintf(b, "- +%d / −%d lines\n", s.Additions, s.Deletions)
	}
	if s.MRsOpened > 0 {
		fmt.Fprintf(b, "- %s opened\n", mrPhrase(s.MRsOpened))
	}
	if s.MRsMerged > 0 {
		fmt.Fprintf(b, "- %s merged\n", mrPhrase(s.MRsMerged))
	}
	if s.MRsClosed > 0 {
		fmt.Fprintf(b, "- %s closed\n", mrPhrase(s.MRsClosed))
	}
	if s.MRsUpdated > 0 {
		fmt.Fprintf(b, "- %s updated\n", mrPhrase(s.MRsUpdated))
	}
	if s.Issues > 0 {
		fmt.Fprintf(b, "- %d %s\n", s.Issues, pluralize(s.Issues, "issue update", "issue updates"))
	}
	if s.Reviews > 0 {
		fmt.Fprintf(b, "- %d %s\n", s.Reviews, pluralize(s.Reviews, "review comment", "review comments"))
	}
	b.WriteString("\n")
}

func writeGlance(b *bytes.Buffer, rep *Report) {
	if len(rep.Projects) == 0 {
		return
	}
	b.WriteString("### Projects at a glance\n\n")
	for _, project := range rep.Projects {
		fmt.Fprintf(b, "- **%s** — %s\n", project.PathWithNamespace, glanceLine(project))
	}
	b.WriteString("\n")
}

func glanceLine(project Project) string {
	var parts []string

	if len(project.Commits) > 0 {
		phrase := fmt.Sprintf("%d %s", len(project.Commits), pluralize(len(project.Commits), "commit", "commits"))
		switch len(project.Branches) {
		case 0:
		case 1:
			phrase += fmt.Sprintf(" on `%s`", project.Branches[0])
		default:
			phrase += " on " + codeList(project.Branches)
		}
		parts = append(parts, phrase)
	}
	if len(project.Tags) > 0 {
		label := "tag"
		if len(project.Tags) > 1 {
			label = "tags"
		}
		parts = append(parts, label+" "+codeList(project.Tags))
	}
	if len(project.Branches) > 0 && len(project.Commits) == 0 && len(project.Tags) == 0 {
		parts = append(parts, "branch "+codeList(project.Branches))
	}
	if n := countMRs(project.MergeRequests, ActionOpened); n > 0 {
		parts = append(parts, mrPhrase(n)+" opened")
	}
	if n := countMRs(project.MergeRequests, ActionMerged); n > 0 {
		parts = append(parts, mrPhrase(n)+" merged")
	}
	if n := countMRs(project.MergeRequests, ActionClosed); n > 0 {
		parts = append(parts, mrPhrase(n)+" closed")
	}
	if n := countMRs(project.MergeRequests, ActionUpdated); n > 0 {
		parts = append(parts, mrPhrase(n)+" updated")
	}
	if len(project.Reviews) > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", len(project.Reviews), pluralize(len(project.Reviews), "review", "reviews")))
	}
	if n := len(project.Issues); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, pluralize(n, "issue update", "issue updates")))
	}
	if len(parts) == 0 && project.Pushes > 0 {
		parts = append(parts, fmt.Sprintf("%d %s (details unavailable)", project.Pushes, pluralize(project.Pushes, "push", "pushes")))
	}
	return joinPhrases(parts)
}

func writeProject(b *bytes.Buffer, rep *Report, project Project) {
	fmt.Fprintf(b, "## %s\n\n", project.PathWithNamespace)
	if project.WebURL != "" {
		fmt.Fprintf(b, "%s\n\n", project.WebURL)
	}
	if len(project.Branches) > 0 {
		fmt.Fprintf(b, "**Branches:** %s\n\n", codeList(project.Branches))
	}
	if len(project.Tags) > 0 {
		fmt.Fprintf(b, "**Tags:** %s\n\n", codeList(project.Tags))
	}
	if project.UnresolvedPushes > 0 {
		fmt.Fprintf(b, "_%d %s could not be expanded to commits._\n\n",
			project.UnresolvedPushes, pluralize(project.UnresolvedPushes, "push", "pushes"))
	}

	if len(project.Commits) > 0 {
		writeCommits(b, rep, project.Commits)
	}
	if len(project.MergeRequests) > 0 {
		writeMergeRequests(b, project.MergeRequests)
	}
	if len(project.Reviews) > 0 {
		writeReviews(b, project.Reviews)
	}
	if len(project.Issues) > 0 {
		writeIssues(b, rep, project.Issues)
	}
}

func writeCommits(b *bytes.Buffer, rep *Report, commits []Commit) {
	b.WriteString("### Commits\n\n")
	for _, commit := range commits {
		sha := "`" + commit.ShortID + "`"
		if commit.WebURL != "" {
			sha = fmt.Sprintf("[`%s`](%s)", commit.ShortID, commit.WebURL)
		}
		line := fmt.Sprintf("- `%s` %s — %s", clock(rep, commit.PushedAt), sha, cleanInline(commit.Title, 120))
		if commit.HasStats {
			line += fmt.Sprintf(" (+%d / −%d)", commit.Additions, commit.Deletions)
		}
		if !commit.AuthorIsUser && commit.AuthorName != "" {
			line += fmt.Sprintf(" _(by %s)_", cleanInline(commit.AuthorName, 60))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func writeMergeRequests(b *bytes.Buffer, mrs []MergeRequest) {
	b.WriteString("### Merge requests\n\n")
	for _, mr := range mrs {
		line := "- " + itemRef(mr.IID, mr.WebURL)
		if title := cleanInline(mr.Title, 120); title != "" {
			line += " — " + title
		}
		line += fmt.Sprintf(" — **%s**", mr.Action)
		if mr.CommentCount > 0 {
			line += fmt.Sprintf(" · %d %s", mr.CommentCount, pluralize(mr.CommentCount, "comment", "comments"))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func writeReviews(b *bytes.Buffer, reviews []Review) {
	b.WriteString("### Reviews\n\n")
	for _, review := range reviews {
		line := "- Reviewed " + itemRef(review.IID, review.WebURL)
		if title := cleanInline(review.Title, 120); title != "" {
			line += " — " + title
		}
		if review.CommentCount > 0 {
			line += fmt.Sprintf(" — %d %s", review.CommentCount, pluralize(review.CommentCount, "comment", "comments"))
		}
		b.WriteString(line + "\n")
		for i, excerpt := range review.Excerpts {
			if i >= 2 {
				break
			}
			fmt.Fprintf(b, "  - _%s_\n", cleanInline(excerpt, 160))
		}
	}
	b.WriteString("\n")
}

func writeIssues(b *bytes.Buffer, rep *Report, issues []Issue) {
	b.WriteString("### Issues\n\n")
	for _, issue := range issues {
		ref := itemRef(issue.IID, issue.WebURL)
		title := cleanInline(issue.Title, 120)
		if issue.Action == "commented" {
			line := fmt.Sprintf("- `%s` %s", clock(rep, issue.At), ref)
			if title != "" {
				line += " — " + title
			}
			if issue.Note != "" {
				line += fmt.Sprintf(" — _%s_", cleanInline(issue.Note, 160))
			}
			b.WriteString(line + "\n")
			continue
		}
		line := "- " + ref
		if title != "" {
			line += " — " + title
		}
		line += fmt.Sprintf(" — **%s**", issue.Action)
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func itemRef(iid int, webURL string) string {
	if webURL == "" {
		return fmt.Sprintf("!%d", iid)
	}
	return fmt.Sprintf("[!%d](%s)", iid, webURL)
}

func clock(rep *Report, t time.Time) string {
	loc := rep.Location
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("15:04")
}

// cleanInline collapses whitespace, truncates and escapes Markdown link
// brackets so a title can never break the surrounding list item.
func cleanInline(s string, limit int) string {
	s = clip(collapseWhitespace(s), limit)
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return s
}

func codeList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "`" + value + "`"
	}
	return strings.Join(quoted, ", ")
}

func mrPhrase(n int) string {
	return fmt.Sprintf("%d %s", n, pluralize(n, "MR", "MRs"))
}

func countMRs(mrs []MergeRequest, action Action) int {
	count := 0
	for _, mr := range mrs {
		if mr.Action == action {
			count++
		}
	}
	return count
}

func joinPhrases(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
