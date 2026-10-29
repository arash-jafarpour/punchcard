package report

import (
	"sort"
	"strconv"
	"time"

	"github.com/arash-jafarpour/punchcard/internal/date"
)

// MultiDay is the combined report for a window of consecutive days. Days are
// ordered oldest first and keep days without activity, thus the merged summary
// and the document name always cover the whole requested window.
type MultiDay struct {
	// Ranges are the ordered day windows that produced Days.
	Ranges []date.Range
	// Days are the per-day reports, in the same order as Ranges.
	Days []*Report

	User     UserInfo
	Timezone string
	Location *time.Location
	Summary  Summary
	Warnings []string
}

// NewMultiDay combines the ordered per-day reports of a window. Ranges must
// describe the same days as days; they name the document and its label.
func NewMultiDay(ranges []date.Range, days []*Report) *MultiDay {
	md := &MultiDay{Ranges: ranges, Days: days}
	if len(days) > 0 {
		first := days[0]
		md.User = first.User
		md.Timezone = first.Timezone
		md.Location = first.Location
	}
	md.Summary = mergeSummaries(days, len(md.Projects()))
	md.Warnings = mergeWarnings(days)
	return md
}

// Stem is the report file name stem: YYYY-MM-DD for one day and
// YYYY-MM-DD_YYYY-MM-DD for a range.
func (m *MultiDay) Stem() string { return date.WindowStem(m.Ranges) }

// Label is the inclusive human window, used in the title and the caption.
func (m *MultiDay) Label() string { return date.WindowLabel(m.Ranges) }

// Projects merges the per-day project sections into one entry per GitLab
// project, sorted by path. The aggregate glance and the project count thus
// count a project once, not once per day it appeared on.
func (m *MultiDay) Projects() []Project {
	type accum struct {
		project  Project
		branches map[string]struct{}
		tags     map[string]struct{}
	}

	byKey := make(map[string]*accum)
	var keys []string
	for _, day := range m.Days {
		for _, source := range day.Projects {
			key := projectIdentity(source)
			entry := byKey[key]
			if entry == nil {
				entry = &accum{
					project: Project{
						ID:                source.ID,
						PathWithNamespace: source.PathWithNamespace,
						Name:              source.Name,
						WebURL:            source.WebURL,
						Available:         source.Available,
					},
					branches: make(map[string]struct{}),
					tags:     make(map[string]struct{}),
				}
				byKey[key] = entry
				keys = append(keys, key)
			}
			merged := &entry.project
			merged.Pushes += source.Pushes
			merged.UnresolvedPushes += source.UnresolvedPushes
			merged.Commits = append(merged.Commits, source.Commits...)
			merged.MergeRequests = append(merged.MergeRequests, source.MergeRequests...)
			merged.Reviews = append(merged.Reviews, source.Reviews...)
			merged.Issues = append(merged.Issues, source.Issues...)
			for _, branch := range source.Branches {
				entry.branches[branch] = struct{}{}
			}
			for _, tag := range source.Tags {
				entry.tags[tag] = struct{}{}
			}
		}
	}

	projects := make([]Project, 0, len(keys))
	for _, key := range keys {
		entry := byKey[key]
		entry.project.Branches = sortedSet(entry.branches)
		entry.project.Tags = sortedSet(entry.tags)
		projects = append(projects, entry.project)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].PathWithNamespace < projects[j].PathWithNamespace
	})
	return projects
}

// projectIdentity names a project across days. The id survives a rename,
// while the path is the fallback for a section without metadata.
func projectIdentity(project Project) string {
	if project.ID > 0 {
		return "id:" + strconv.Itoa(project.ID)
	}
	return "path:" + project.PathWithNamespace
}

// mergeSummaries sums the per-day counts. Project is the number of unique
// projects in the window, not the sum of the daily project counts.
func mergeSummaries(days []*Report, projects int) Summary {
	summary := Summary{Projects: projects}
	for _, day := range days {
		s := day.Summary
		summary.Pushes += s.Pushes
		summary.Commits += s.Commits
		summary.Additions += s.Additions
		summary.Deletions += s.Deletions
		summary.MRsOpened += s.MRsOpened
		summary.MRsMerged += s.MRsMerged
		summary.MRsClosed += s.MRsClosed
		summary.MRsUpdated += s.MRsUpdated
		summary.Issues += s.Issues
		summary.Reviews += s.Reviews
	}
	return summary
}

// mergeWarnings tags every warning with its day, so a repeated warning from
// two days stays distinguishable, and keeps the list deterministic.
func mergeWarnings(days []*Report) []string {
	var warnings []string
	for _, day := range days {
		for _, warning := range day.Warnings {
			warnings = append(warnings, day.Date+": "+warning)
		}
	}
	sort.Strings(warnings)
	return warnings
}
