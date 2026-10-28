package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arash-jafarpour/punchcard/internal/date"
	"github.com/arash-jafarpour/punchcard/internal/gitlab"
)

const (
	defaultConcurrency = 5
	defaultMaxMRNotes  = 50
	maxExcerptsPerMR   = 3
	reviewExcerptLimit = 200
)

// Options tunes collection without changing its output shape.
type Options struct {
	// AuthorEmails, when non-empty, restricts commits to these commit-author
	// emails. Empty means no filtering.
	AuthorEmails []string
	// MaxMRsWithNotes caps per-MR note enrichment to keep API usage bounded.
	MaxMRsWithNotes int
	// Concurrency bounds concurrent commit enrichment.
	Concurrency int
}

func (o Options) withDefaults() Options {
	if o.Concurrency <= 0 {
		o.Concurrency = defaultConcurrency
	}
	if o.MaxMRsWithNotes <= 0 {
		o.MaxMRsWithNotes = defaultMaxMRNotes
	}
	return o
}

// Collector turns raw GitLab data into a Report.
type Collector struct {
	client *gitlab.Client
	log    *slog.Logger
}

// NewCollector returns a Collector. A nil logger falls back to
// slog.Default(), which the CLI replaces with its own handler.
func NewCollector(client *gitlab.Client, log *slog.Logger) *Collector {
	if log == nil {
		log = slog.Default()
	}
	return &Collector{client: client, log: log}
}

// Collect gathers every activity type for the given day.
func (c *Collector) Collect(ctx context.Context, rng date.Range, user *gitlab.User, opts Options) (*Report, error) {
	if user == nil {
		return nil, errors.New("report: current user is required")
	}
	opts = opts.withDefaults()

	rep := &Report{
		Date:     rng.Date(),
		Timezone: rng.Timezone,
		Location: rng.Location,
		User:     UserInfo{Name: user.Name, Username: user.Username},
	}

	var warnings []string
	warn := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		warnings = append(warnings, msg)
		c.log.Warn(msg)
	}

	events, err := c.client.Events(ctx, rng.EventsAfter(), rng.EventsBefore())
	if err != nil {
		return nil, fmt.Errorf("collect events: %w", err)
	}

	agg := newAggregate()
	var works []pushWork

	for _, ev := range events {
		// Keep only events this user performed. The default feed is already
		// per-user, but this also guards against instance or version
		// differences (scope=all, for example, includes colleagues' activity).
		if !strings.EqualFold(strings.TrimSpace(ev.AuthorUsername), user.Username) {
			continue
		}
		// The API window is date-only and inclusive of neighbouring days at the
		// edges; pin every event to the report day in the report timezone.
		if !rng.Contains(ev.CreatedAt) {
			continue
		}
		switch {
		case ev.PushData != nil:
			if ev.ProjectID <= 0 {
				warn("push event %d has no project id", ev.ID)
				continue
			}
			pa := agg.project(ev.ProjectID)
			pa.pushes++
			work, ok := workFromPush(ev)
			if !ok {
				pa.unresolved++
				warn("push event %d in project %d has no ref or commit details", ev.ID, ev.ProjectID)
				continue
			}
			if work.ref != "" {
				if strings.EqualFold(work.refType, "tag") {
					pa.tags[work.ref] = struct{}{}
				} else {
					pa.branches[work.ref] = struct{}{}
				}
			}
			works = append(works, work)

		case ev.TargetType == "Issue":
			if ev.ProjectID <= 0 {
				continue
			}
			pa := agg.project(ev.ProjectID)
			pa.issues = append(pa.issues, issueSeed{
				iid:    ev.TargetIID,
				title:  ev.TargetTitle,
				action: issueAction(ev.ActionName),
				at:     ev.CreatedAt,
			})

		case ev.TargetType == "Note" && ev.Note != nil:
			if ev.Note.System || ev.Note.NoteableType != "Issue" || ev.ProjectID <= 0 {
				continue
			}
			iid := ev.Note.NoteableIID
			if iid == 0 {
				iid = ev.TargetIID
			}
			at := ev.Note.CreatedAt
			if at.IsZero() {
				at = ev.CreatedAt
			}
			pa := agg.project(ev.ProjectID)
			pa.issues = append(pa.issues, issueSeed{
				iid:    iid,
				title:  ev.TargetTitle,
				action: "commented",
				note:   clip(collapseWhitespace(ev.Note.Body), reviewExcerptLimit),
				at:     at,
			})
		}
	}

	results, workWarnings, unresolved := c.runPushWork(ctx, rng, works, opts.Concurrency)
	for _, msg := range workWarnings {
		warn("%s", msg)
	}
	for projectID, count := range unresolved {
		agg.project(projectID).unresolved += count
	}

	allowed := emailSet(opts.AuthorEmails)
	isUserAuthor := func(email string) bool {
		if len(allowed) > 0 {
			_, ok := allowed[strings.ToLower(strings.TrimSpace(email))]
			return ok
		}
		if user.Email == "" {
			return true
		}
		return strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(user.Email))
	}

	excluded := c.integrateCommits(agg, results, allowed)
	if excluded > 0 {
		warn("%d commits excluded by GITLAB_AUTHOR_EMAILS", excluded)
	}

	if err := c.collectMergeRequests(ctx, rng, user, agg, opts, warn); err != nil {
		return nil, err
	}

	rep.Projects = c.buildProjects(ctx, rng, agg, isUserAuthor, warn)
	rep.Summary = summarize(agg, rep.Projects, rng)
	sort.Strings(warnings)
	rep.Warnings = warnings
	return rep, nil
}

// integrateCommits deduplicates commit results by project + SHA and attaches
// them to their projects. It returns how many commits the author filter
// excluded.
func (c *Collector) integrateCommits(agg *aggregate, results []commitResult, allowed map[string]struct{}) int {
	excluded := 0
	excludedKeys := make(map[commitKey]struct{})
	for _, res := range results {
		key := commitKey{projectID: res.projectID, sha: res.commit.ID}
		if existing, ok := agg.commits[key]; ok {
			existing.merge(res)
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(strings.TrimSpace(res.commit.AuthorEmail))]; !ok {
				// Count unique commits, not every push occurrence.
				if _, seen := excludedKeys[key]; !seen {
					excludedKeys[key] = struct{}{}
					excluded++
				}
				continue
			}
		}
		acc := &commitAccum{commit: res.commit, pushedAt: res.pushedAt, precise: res.precise}
		acc.addRef(res.ref)
		agg.commits[key] = acc
		agg.project(res.projectID).commits = append(agg.project(res.projectID).commits, acc)
	}
	return excluded
}

// collectMergeRequests merges authored and reviewed MRs and enriches them with
// the user's notes inside the day.
func (c *Collector) collectMergeRequests(ctx context.Context, rng date.Range, user *gitlab.User, agg *aggregate, opts Options, warn func(string, ...any)) error {
	authored, err := c.client.MergeRequests(ctx, gitlab.MergeRequestQuery{
		AuthorUsername: user.Username,
		UpdatedAfter:   rng.StartTimestamp(),
		UpdatedBefore:  rng.EndTimestamp(),
	})
	if err != nil {
		return fmt.Errorf("collect authored merge requests: %w", err)
	}
	reviewed, err := c.client.ReviewerMergeRequests(ctx, user.Username, user.ID, rng.StartTimestamp(), rng.EndTimestamp())
	if err != nil {
		return fmt.Errorf("collect reviewed merge requests: %w", err)
	}

	for _, mr := range authored {
		if mr.ProjectID <= 0 || mr.IID <= 0 {
			warn("authored merge request %s has no project id", mr.References.Full)
			continue
		}
		agg.addMR(mr, roleAuthor)
	}
	for _, mr := range reviewed {
		if mr.ProjectID <= 0 || mr.IID <= 0 {
			warn("reviewed merge request %s has no project id", mr.References.Full)
			continue
		}
		agg.addMR(mr, roleReviewer)
	}

	keys := agg.mrKeys()
	processed := 0
	for _, key := range keys {
		if processed >= opts.MaxMRsWithNotes {
			warn("review note enrichment capped at %d merge requests; %d skipped", opts.MaxMRsWithNotes, len(keys)-processed)
			break
		}
		processed++
		acc := agg.mrs[key]
		notes, err := c.client.MergeRequestNotes(ctx, key.projectID, key.iid)
		if err != nil {
			warn("merge request !%d in project %d notes: %v", key.iid, key.projectID, err)
			continue
		}
		for _, note := range notes {
			if note.System || !rng.Contains(note.CreatedAt) {
				continue
			}
			if !strings.EqualFold(note.Author.Username, user.Username) {
				continue
			}
			acc.commentCount++
			if len(acc.excerpts) < maxExcerptsPerMR {
				acc.excerpts = append(acc.excerpts, clip(collapseWhitespace(note.Body), reviewExcerptLimit))
			}
		}
		agg.totalComments += acc.commentCount
	}
	return nil
}

// buildProjects fetches metadata and assembles deterministic project sections.
func (c *Collector) buildProjects(ctx context.Context, rng date.Range, agg *aggregate, isUserAuthor func(string) bool, warn func(string, ...any)) []Project {
	ids := agg.projectIDs()
	projects := make([]Project, 0, len(ids))

	for _, id := range ids {
		pa := agg.projects[id]
		if !pa.hasActivity() {
			continue
		}

		project := Project{ID: id, Pushes: pa.pushes, UnresolvedPushes: pa.unresolved}
		meta, err := c.client.Project(ctx, id)
		if err != nil {
			warn("project %d metadata unavailable: %v", id, err)
		} else {
			project.Available = true
			project.Name = meta.Name
			project.PathWithNamespace = meta.PathWithNamespace
			project.WebURL = meta.WebURL
		}
		if project.PathWithNamespace == "" {
			project.PathWithNamespace = fmt.Sprintf("project #%d", id)
		}
		if project.Name == "" {
			project.Name = project.PathWithNamespace
		}

		project.Branches = sortedSet(pa.branches)
		project.Tags = sortedSet(pa.tags)

		sort.Slice(pa.commits, func(i, j int) bool {
			if !pa.commits[i].pushedAt.Equal(pa.commits[j].pushedAt) {
				return pa.commits[i].pushedAt.Before(pa.commits[j].pushedAt)
			}
			return pa.commits[i].commit.ID < pa.commits[j].commit.ID
		})
		for _, acc := range pa.commits {
			project.Commits = append(project.Commits, modelCommit(acc, project.WebURL, isUserAuthor(acc.commit.AuthorEmail)))
		}

		for _, acc := range sortedMRs(pa.mrs) {
			if acc.hasRole(roleAuthor) {
				project.MergeRequests = append(project.MergeRequests, MergeRequest{
					IID:          acc.mr.IID,
					Title:        acc.mr.Title,
					WebURL:       acc.mr.WebURL,
					Action:       mrAction(acc.mr, rng),
					CommentCount: acc.commentCount,
				})
				continue
			}
			project.Reviews = append(project.Reviews, Review{
				IID:          acc.mr.IID,
				Title:        acc.mr.Title,
				WebURL:       acc.mr.WebURL,
				CommentCount: acc.commentCount,
				Excerpts:     acc.excerpts,
			})
		}

		sort.Slice(pa.issues, func(i, j int) bool {
			if !pa.issues[i].at.Equal(pa.issues[j].at) {
				return pa.issues[i].at.Before(pa.issues[j].at)
			}
			return pa.issues[i].iid < pa.issues[j].iid
		})
		for _, seed := range pa.issues {
			url := ""
			if project.WebURL != "" {
				url = fmt.Sprintf("%s/-/issues/%d", project.WebURL, seed.iid)
			}
			project.Issues = append(project.Issues, Issue{
				IID:    seed.iid,
				Title:  seed.title,
				WebURL: url,
				Action: seed.action,
				Note:   seed.note,
				At:     seed.at,
			})
		}

		projects = append(projects, project)
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].PathWithNamespace < projects[j].PathWithNamespace
	})
	return projects
}

func summarize(agg *aggregate, projects []Project, rng date.Range) Summary {
	summary := Summary{Projects: len(projects), Reviews: agg.totalComments}
	for _, project := range projects {
		summary.Pushes += project.Pushes
		summary.Commits += len(project.Commits)
		summary.Issues += len(project.Issues)
		for _, commit := range project.Commits {
			if commit.HasStats {
				summary.Additions += commit.Additions
				summary.Deletions += commit.Deletions
			}
		}
	}
	for _, key := range agg.mrKeys() {
		switch mrAction(agg.mrs[key].mr, rng) {
		case ActionOpened:
			summary.MRsOpened++
		case ActionMerged:
			summary.MRsMerged++
		case ActionClosed:
			summary.MRsClosed++
		default:
			summary.MRsUpdated++
		}
	}
	return summary
}

// --- push expansion -------------------------------------------------------

type workKind int

const (
	workSingle workKind = iota
	workCompare
	workList
)

type pushWork struct {
	projectID   int
	kind        workKind
	ref         string
	refType     string
	sha         string
	from        string
	to          string
	commitTitle string
	pushedAt    time.Time
}

func workFromPush(ev gitlab.Event) (pushWork, bool) {
	pd := ev.PushData
	if pd == nil || ev.ProjectID <= 0 {
		return pushWork{}, false
	}
	work := pushWork{
		projectID:   ev.ProjectID,
		ref:         cleanRef(pd.Ref),
		refType:     pd.RefType,
		commitTitle: pd.CommitTitle,
		pushedAt:    ev.CreatedAt,
	}
	switch {
	case pd.CommitCount == 1 && isCommitSHA(pd.CommitTo):
		work.kind = workSingle
		work.sha = pd.CommitTo
		return work, true
	case pd.CommitCount > 1 && isCommitSHA(pd.CommitFrom) && isCommitSHA(pd.CommitTo):
		work.kind = workCompare
		work.from = pd.CommitFrom
		work.to = pd.CommitTo
		return work, true
	case work.ref != "":
		work.kind = workList
		return work, true
	}
	return pushWork{}, false
}

type commitResult struct {
	projectID int
	ref       string
	pushedAt  time.Time
	// precise is true when the push event named this commit (single or
	// compare). List fallbacks can contain commits that a later push carried,
	// so their timestamp is only an approximation.
	precise bool
	commit  gitlab.Commit
}

func (c *Collector) runPushWork(ctx context.Context, rng date.Range, works []pushWork, concurrency int) ([]commitResult, []string, map[int]int) {
	if len(works) == 0 {
		return nil, nil, nil
	}
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	var (
		mu         sync.Mutex
		wg         sync.WaitGroup
		results    []commitResult
		warnings   []string
		unresolved = make(map[int]int)
	)
	sem := make(chan struct{}, concurrency)

	for _, work := range works {
		work := work
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			commits, warns := c.expandPush(ctx, rng, work)

			mu.Lock()
			defer mu.Unlock()
			warnings = append(warnings, warns...)
			if len(commits) == 0 {
				unresolved[work.projectID]++
				return
			}
			results = append(results, commits...)
		}()
	}
	wg.Wait()
	return results, warnings, unresolved
}

func (c *Collector) expandPush(ctx context.Context, rng date.Range, work pushWork) ([]commitResult, []string) {
	var warnings []string

	if work.kind == workSingle {
		commit, err := c.client.Commit(ctx, work.projectID, work.sha)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("commit %s in project %d: %v", shortSHA(work.sha), work.projectID, err))
			return []commitResult{{
				projectID: work.projectID,
				ref:       work.ref,
				pushedAt:  work.pushedAt,
				precise:   true,
				commit: gitlab.Commit{
					ID:        work.sha,
					ShortID:   shortSHA(work.sha),
					Title:     work.commitTitle,
					CreatedAt: work.pushedAt,
				},
			}}, warnings
		}
		return []commitResult{{
			projectID: work.projectID,
			ref:       work.ref,
			pushedAt:  work.pushedAt,
			precise:   true,
			commit:    *commit,
		}}, nil
	}

	if work.kind == workCompare {
		commits, err := c.client.Compare(ctx, work.projectID, work.from, work.to)
		switch {
		case err == nil && len(commits) > 0:
			results, more := c.fetchCommitDetails(ctx, work, commits, true)
			return results, append(warnings, more...)
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("compare %s..%s in project %d failed: %v", shortSHA(work.from), shortSHA(work.to), work.projectID, err))
		default:
			warnings = append(warnings, fmt.Sprintf("compare %s..%s in project %d returned no commits", shortSHA(work.from), shortSHA(work.to), work.projectID))
		}
		if work.ref == "" {
			return nil, warnings
		}
	}

	if work.ref == "" {
		return nil, append(warnings, fmt.Sprintf("push in project %d has no ref to expand", work.projectID))
	}

	commits, err := c.client.Commits(ctx, work.projectID, work.ref, rng.Start, rng.End)
	if err != nil {
		return nil, append(warnings, fmt.Sprintf("list commits on %s in project %d: %v", work.ref, work.projectID, err))
	}
	if len(commits) == 0 {
		return nil, append(warnings, fmt.Sprintf("no commits found for %s in project %d", work.ref, work.projectID))
	}
	results, more := c.fetchCommitDetails(ctx, work, commits, false)
	return results, append(warnings, more...)
}

// fetchCommitDetails fills in stats and short ids with one call per commit.
func (c *Collector) fetchCommitDetails(ctx context.Context, work pushWork, commits []gitlab.Commit, precise bool) ([]commitResult, []string) {
	results := make([]commitResult, 0, len(commits))
	var warnings []string
	for _, commit := range commits {
		if commit.ID == "" {
			continue
		}
		full := commit
		if detail, err := c.client.Commit(ctx, work.projectID, commit.ID); err == nil {
			full = *detail
		} else {
			warnings = append(warnings, fmt.Sprintf("commit %s in project %d: %v", shortSHA(commit.ID), work.projectID, err))
		}
		if full.ShortID == "" {
			full.ShortID = shortSHA(full.ID)
		}
		results = append(results, commitResult{
			projectID: work.projectID,
			ref:       work.ref,
			pushedAt:  work.pushedAt,
			precise:   precise,
			commit:    full,
		})
	}
	return results, warnings
}

func modelCommit(acc *commitAccum, projectURL string, authorIsUser bool) Commit {
	c := Commit{
		ShortID:      acc.commit.ShortID,
		SHA:          acc.commit.ID,
		Title:        acc.commit.Title,
		WebURL:       acc.commit.WebURL,
		PushedAt:     acc.pushedAt,
		AuthorName:   acc.commit.AuthorName,
		AuthorIsUser: authorIsUser,
		Refs:         acc.refs,
	}
	if c.ShortID == "" {
		c.ShortID = shortSHA(c.SHA)
	}
	if c.Title == "" {
		c.Title = firstLine(acc.commit.Message)
	}
	if c.WebURL == "" && projectURL != "" && c.SHA != "" {
		c.WebURL = projectURL + "/-/commit/" + c.SHA
	}
	if stats := acc.commit.Stats; stats != nil {
		c.HasStats = true
		c.Additions = stats.Additions
		c.Deletions = stats.Deletions
	}
	return c
}

// --- aggregation ----------------------------------------------------------

type commitKey struct {
	projectID int
	sha       string
}

type mrKey struct {
	projectID int
	iid       int
}

type mrRole string

const (
	roleAuthor   mrRole = "author"
	roleReviewer mrRole = "reviewer"
)

type commitAccum struct {
	commit   gitlab.Commit
	refs     []string
	pushedAt time.Time
	precise  bool
}

func (c *commitAccum) addRef(ref string) {
	if ref == "" {
		return
	}
	for _, existing := range c.refs {
		if existing == ref {
			return
		}
	}
	c.refs = append(c.refs, ref)
}

// merge combines duplicate observations of one commit. The rule is
// order-independent: a precise push time always beats a listing approximation,
// and the earliest precise time wins.
func (c *commitAccum) merge(other commitResult) {
	switch {
	case other.precise && !c.precise:
		c.pushedAt = other.pushedAt
		c.precise = true
	case other.precise && c.precise:
		if other.pushedAt.Before(c.pushedAt) {
			c.pushedAt = other.pushedAt
		}
	case !other.precise && !c.precise:
		if other.pushedAt.Before(c.pushedAt) {
			c.pushedAt = other.pushedAt
		}
	}
	c.addRef(other.ref)
	if c.commit.Stats == nil && other.commit.Stats != nil {
		c.commit.Stats = other.commit.Stats
	}
	if c.commit.ShortID == "" {
		c.commit.ShortID = other.commit.ShortID
	}
}

type mrAccum struct {
	mr           gitlab.MergeRequest
	roles        map[mrRole]struct{}
	commentCount int
	excerpts     []string
}

func (m *mrAccum) hasRole(role mrRole) bool {
	_, ok := m.roles[role]
	return ok
}

type issueSeed struct {
	iid    int
	title  string
	action string
	note   string
	at     time.Time
}

type projectAgg struct {
	id         int
	pushes     int
	unresolved int
	branches   map[string]struct{}
	tags       map[string]struct{}
	commits    []*commitAccum
	mrs        map[mrKey]*mrAccum
	issues     []issueSeed
}

func (p *projectAgg) hasActivity() bool {
	return p.pushes > 0 || len(p.commits) > 0 || len(p.mrs) > 0 || len(p.issues) > 0
}

type aggregate struct {
	projects      map[int]*projectAgg
	commits       map[commitKey]*commitAccum
	mrs           map[mrKey]*mrAccum
	totalComments int
}

func newAggregate() *aggregate {
	return &aggregate{
		projects: make(map[int]*projectAgg),
		commits:  make(map[commitKey]*commitAccum),
		mrs:      make(map[mrKey]*mrAccum),
	}
}

func (a *aggregate) project(id int) *projectAgg {
	pa := a.projects[id]
	if pa == nil {
		pa = &projectAgg{
			id:       id,
			branches: make(map[string]struct{}),
			tags:     make(map[string]struct{}),
			mrs:      make(map[mrKey]*mrAccum),
		}
		a.projects[id] = pa
	}
	return pa
}

func (a *aggregate) addMR(mr gitlab.MergeRequest, role mrRole) *mrAccum {
	key := mrKey{projectID: mr.ProjectID, iid: mr.IID}
	acc := a.mrs[key]
	if acc == nil {
		acc = &mrAccum{mr: mr, roles: make(map[mrRole]struct{})}
		a.mrs[key] = acc
		a.project(mr.ProjectID).mrs[key] = acc
	}
	acc.roles[role] = struct{}{}
	return acc
}

func (a *aggregate) mrKeys() []mrKey {
	keys := make([]mrKey, 0, len(a.mrs))
	for key := range a.mrs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].projectID != keys[j].projectID {
			return keys[i].projectID < keys[j].projectID
		}
		return keys[i].iid < keys[j].iid
	})
	return keys
}

func (a *aggregate) projectIDs() []int {
	ids := make([]int, 0, len(a.projects))
	for id := range a.projects {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// --- small helpers --------------------------------------------------------

func sortedMRs(mrs map[mrKey]*mrAccum) []*mrAccum {
	out := make([]*mrAccum, 0, len(mrs))
	for _, acc := range mrs {
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mr.IID < out[j].mr.IID })
	return out
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func emailSet(emails []string) map[string]struct{} {
	if len(emails) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		set[strings.ToLower(strings.TrimSpace(email))] = struct{}{}
	}
	return set
}

// isCommitSHA accepts hex object ids and rejects the all-zero sentinel GitLab
// uses for created or deleted refs.
func isCommitSHA(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return strings.Trim(s, "0") != ""
}

func cleanRef(ref string) string {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	return ref
}

func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

func firstLine(message string) string {
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		message = message[:idx]
	}
	return collapseWhitespace(message)
}

func issueAction(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "opened", "closed", "reopened":
		return strings.ToLower(strings.TrimSpace(name))
	default:
		return "updated"
	}
}

func mrAction(mr gitlab.MergeRequest, rng date.Range) Action {
	switch {
	case mr.MergedAt != nil && rng.Contains(*mr.MergedAt):
		return ActionMerged
	case mr.ClosedAt != nil && rng.Contains(*mr.ClosedAt):
		return ActionClosed
	case rng.Contains(mr.CreatedAt):
		return ActionOpened
	default:
		return ActionUpdated
	}
}
