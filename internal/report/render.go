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
	writeGlance(&b, rep.Projects)
	for _, project := range rep.Projects {
		writeProject(&b, rep, project, 2)
	}
	return finish(&b)
}

// RenderMultiDay renders the combined document for a window: the aggregate
// summary once, then one section per day that has activity. Days without
// activity are left out, and project headings drop one level so they sit
// under the day heading.
func RenderMultiDay(md *MultiDay) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Activity report — %s\n\n", md.Label())
	writeIdentity(&b, &Report{User: md.User, Timezone: md.Timezone})

	if !md.Summary.HasActivity() {
		b.WriteString("## No activity\n\n")
		b.WriteString(noActivityLine(md))
		return finish(&b)
	}

	writeSummary(&b, md.Summary)
	writeGlance(&b, md.Projects())
	for _, day := range md.Days {
		if !day.Summary.HasActivity() {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", day.Date)
		for _, project := range day.Projects {
			writeProject(&b, day, project, 3)
		}
	}
	return finish(&b)
}

// noActivityLine says which window produced nothing.
func noActivityLine(md *MultiDay) string {
	switch len(md.Days) {
	case 0:
		return "_No activity recorded._\n"
	case 1:
		return fmt.Sprintf("_No activity recorded for %s._\n", md.Days[0].Date)
	default:
		return fmt.Sprintf("_No activity recorded from %s to %s._\n",
			md.Days[0].Date, md.Days[len(md.Days)-1].Date)
	}
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
	return writeReportFile(dir, rep.Date, Render(rep))
}

// WriteMultiDayFile renders the combined window and writes <stem>.md, where
// the stem is YYYY-MM-DD for one day and YYYY-MM-DD_YYYY-MM-DD for a range.
func WriteMultiDayFile(dir string, md *MultiDay) (string, error) {
	return writeReportFile(dir, md.Stem(), RenderMultiDay(md))
}

func writeReportFile(dir, stem string, data []byte) (string, error) {
	if dir == "" {
		dir = "./reports"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create report directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, stem+".md")
	if err := os.WriteFile(path, data, 0o600); err != nil {
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

func writeGlance(b *bytes.Buffer, projects []Project) {
	if len(projects) == 0 {
		return
	}
	b.WriteString("### Projects at a glance\n\n")
	for _, project := range projects {
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

// writeHeading writes a Markdown heading at level. Level 2 is a top-level
// section of a daily report; level 3 is a project inside a day section.
func writeHeading(b *bytes.Buffer, level int, text string) {
	b.WriteString(strings.Repeat("#", level))
	b.WriteString(" ")
	b.WriteString(text)
	b.WriteString("\n\n")
}

func writeProject(b *bytes.Buffer, rep *Report, project Project, level int) {
	writeHeading(b, level, project.PathWithNamespace)
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
		writeCommits(b, rep, project.Commits, level+1)
	}
	if len(project.MergeRequests) > 0 {
		writeMergeRequests(b, project.MergeRequests, level+1)
	}
	if len(project.Reviews) > 0 {
		writeReviews(b, project.Reviews, level+1)
	}
	if len(project.Issues) > 0 {
		writeIssues(b, rep, project.Issues, level+1)
	}
}

func writeCommits(b *bytes.Buffer, rep *Report, commits []Commit, level int) {
	writeHeading(b, level, "Commits")
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

func writeMergeRequests(b *bytes.Buffer, mrs []MergeRequest, level int) {
	writeHeading(b, level, "Merge requests")
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

func writeReviews(b *bytes.Buffer, reviews []Review, level int) {
	writeHeading(b, level, "Reviews")
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

func writeIssues(b *bytes.Buffer, rep *Report, issues []Issue, level int) {
	writeHeading(b, level, "Issues")
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
