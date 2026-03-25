package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

func (s *Service) ContextBuild(ctx context.Context, req ContextBuildRequest) (ContextBundle, error) {
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return ContextBundle{}, fmt.Errorf("query is required")
	}
	if req.LimitPerSection <= 0 {
		req.LimitPerSection = 5
	}
	recall := func(r RecallRequest) ([]RecallResult, error) {
		r.Query = strings.TrimSpace(r.Query)
		if r.Query == "" {
			return nil, fmt.Errorf("query is required")
		}
		if r.Limit <= 0 {
			r.Limit = req.LimitPerSection
		}
		return s.recallRetrievalUnits(ctx, r, r.Limit)
	}
	bundle := ContextBundle{}
	var err error
	if bundle.ActivePrivateDecisions, err = recall(RecallRequest{
		Query:             req.Query,
		Namespaces:        req.Namespaces,
		IncludePrivate:    true,
		IncludeShared:     false,
		IncludeTranscript: false,
		ProjectKey:        req.ProjectKey,
		BranchName:        req.BranchName,
		UnitKinds:         []string{"decision", "rationale", "design_rationale"},
		Limit:             req.LimitPerSection,
	}); err != nil {
		return ContextBundle{}, err
	}
	if bundle.SharedConstraints, err = recall(RecallRequest{
		Query:             req.Query,
		Namespaces:        req.Namespaces,
		IncludePrivate:    false,
		IncludeShared:     true,
		IncludeTranscript: false,
		ProjectKey:        req.ProjectKey,
		BranchName:        req.BranchName,
		UnitKinds:         []string{"decision", "rationale", "fact"},
		Limit:             req.LimitPerSection,
	}); err != nil {
		return ContextBundle{}, err
	}
	if bundle.RecentDiscussions, err = recall(RecallRequest{
		Query:             req.Query,
		Namespaces:        req.Namespaces,
		IncludePrivate:    false,
		IncludeShared:     false,
		IncludeTranscript: true,
		ProjectKey:        req.ProjectKey,
		BranchName:        req.BranchName,
		Limit:             req.LimitPerSection,
	}); err != nil {
		return ContextBundle{}, err
	}
	if bundle.OpenTasks, err = recall(RecallRequest{
		Query:             req.Query,
		Namespaces:        req.Namespaces,
		IncludePrivate:    true,
		IncludeShared:     true,
		IncludeTranscript: false,
		ProjectKey:        req.ProjectKey,
		BranchName:        req.BranchName,
		UnitKinds:         []string{"task_candidate"},
		Limit:             req.LimitPerSection,
	}); err != nil {
		return ContextBundle{}, err
	}
	rejectedCandidates, err := recall(RecallRequest{
		Query:             req.Query,
		Namespaces:        req.Namespaces,
		IncludePrivate:    true,
		IncludeShared:     true,
		IncludeTranscript: true,
		ProjectKey:        req.ProjectKey,
		BranchName:        req.BranchName,
		Limit:             req.LimitPerSection * 4,
	})
	if err != nil {
		return ContextBundle{}, err
	}
	for _, item := range rejectedCandidates {
		if isContextRejected(item) {
			appendRecallLimited(&bundle.RejectedOptions, item, req.LimitPerSection)
		}
	}
	artifacts, err := s.collectContextArtifacts(ctx, concatRecallResults(
		bundle.ActivePrivateDecisions,
		bundle.SharedConstraints,
		bundle.RecentDiscussions,
		bundle.RejectedOptions,
		bundle.OpenTasks,
	), req.LimitPerSection)
	if err != nil {
		return ContextBundle{}, err
	}
	bundle.Artifacts = artifacts
	return bundle, nil
}

func appendRecallLimited(dst *[]RecallResult, item RecallResult, limit int) {
	if len(*dst) >= limit {
		return
	}
	*dst = append(*dst, item)
}

func concatRecallResults(groups ...[]RecallResult) []RecallResult {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	out := make([]RecallResult, 0, total)
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func isContextDecision(item RecallResult) bool {
	kind := strings.ToLower(item.UnitKind)
	return kind == "decision" || kind == "rationale" || kind == "design_rationale"
}

func isContextRejected(item RecallResult) bool {
	lower := strings.ToLower(item.UnitKind + "\n" + item.Subject + "\n" + item.Body + "\n" + item.LifecycleState)
	return strings.Contains(lower, "rejected_option") || strings.Contains(lower, "rejected") || strings.Contains(lower, "却下") || strings.Contains(lower, "superseded")
}

func isContextTask(item RecallResult) bool {
	kind := strings.ToLower(item.UnitKind + "\n" + item.MemoryType)
	return strings.Contains(kind, "task") || strings.Contains(kind, "todo")
}

func (s *Service) collectContextArtifacts(ctx context.Context, results []RecallResult, limit int) ([]ContextArtifact, error) {
	seen := map[string]ContextArtifact{}
	add := func(uri, title string) {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			return
		}
		if _, ok := seen[uri]; ok {
			return
		}
		seen[uri] = ContextArtifact{URI: uri, Title: strings.TrimSpace(title)}
	}
	for _, item := range results {
		add(item.SourceURI, "")
	}
	if err := s.collectArtifactsFromSpans(ctx, results, add); err != nil {
		return nil, err
	}
	out := make([]ContextArtifact, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].URI < out[j].URI
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Service) collectArtifactsFromSpans(ctx context.Context, results []RecallResult, add func(uri, title string)) error {
	privateIDs := make([]string, 0)
	sharedIDs := make([]string, 0)
	transcriptIDs := make([]string, 0)
	for _, item := range results {
		switch item.MemorySpace {
		case "private":
			privateIDs = append(privateIDs, item.MemoryID)
		case "shared":
			sharedIDs = append(sharedIDs, item.MemoryID)
		case "transcript":
			transcriptIDs = append(transcriptIDs, item.MemoryID)
		}
	}
	for _, spec := range []struct {
		ids         []string
		spanTable   string
		refTable    string
		keyColumn   string
		joinColumn  string
		titleColumn string
	}{
		{privateIDs, "private_artifact_spans", "private_artifact_refs", "memory_id", "artifact_id", "title"},
		{sharedIDs, "artifact_spans", "artifact_refs", "memory_id", "artifact_id", "title"},
		{transcriptIDs, "transcript_artifact_spans", "private_artifact_refs", "chunk_id", "artifact_id", "title"},
	} {
		if err := collectArtifactRows(ctx, s.db, spec.ids, spec.spanTable, spec.refTable, spec.keyColumn, spec.joinColumn, spec.titleColumn, add); err != nil {
			return err
		}
	}
	return nil
}

func collectArtifactRows(ctx context.Context, db *sql.DB, ids []string, spanTable, refTable, keyColumn, joinColumn, titleColumn string, add func(uri, title string)) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	query := fmt.Sprintf(`
		SELECT DISTINCT COALESCE(r.uri, ''), COALESCE(r.%s, '')
		FROM %s s
		JOIN %s r ON r.%s = s.artifact_id
		WHERE s.%s IN (%s)
	`, titleColumn, spanTable, refTable, joinColumn, keyColumn, strings.Join(placeholders, ","))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var uri, title string
		if err := rows.Scan(&uri, &title); err != nil {
			return err
		}
		add(uri, title)
	}
	return rows.Err()
}
