package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/embedding"
	"crdt-agent-memory/internal/signing"
)

type Service struct {
	db      *sql.DB
	signer  signing.Signer
	vecOnce sync.Once
	vecOK   bool
	vecErr  error
}

func NewService(db *sql.DB, signer signing.Signer) *Service {
	return &Service{db: db, signer: signer}
}

func (s *Service) Store(ctx context.Context, req StoreRequest) (string, error) {
	req, signature, err := s.prepareStore(req)
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if err := s.storeTx(ctx, tx, req, signature); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return req.MemoryID, nil
}

// Recall is a fused lexical+semantic+trust+graph+artifact reranker when
// sqlite-vec is available, and degrades to lexical+trust+graph+artifact when
// it is not.
func (s *Service) recallWithVector(ctx context.Context, req RecallRequest, limit int) ([]RecallResult, error) {
	candidateLimit := limit * 10
	if candidateLimit < 50 {
		candidateLimit = 50
	}
	if candidateLimit < limit {
		candidateLimit = limit
	}
	vector, err := embedding.FromText(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	vectorJSON, err := json.Marshal(vector)
	if err != nil {
		return nil, err
	}
	query := `
		WITH candidate AS (
			SELECT
				memory_space,
				memory_id,
				distance
			FROM memory_embedding_vectors
			WHERE embedding MATCH vec_f32(?)
			  AND k = ?
	`
	args := []any{string(vectorJSON), candidateLimit}
	if !req.IncludePrivate {
		query += ` AND memory_space = 'shared'`
	}
	query += `
			ORDER BY distance
		)
		SELECT
			v.memory_space,
			v.memory_id,
			v.namespace,
			v.memory_type,
			v.subject,
			v.body,
			v.lifecycle_state,
			v.authored_at_ms,
			v.source_uri,
			v.source_hash,
			v.origin_peer_id
		FROM candidate c
		JOIN recall_memory_view v
		  ON v.memory_space = c.memory_space
		 AND v.memory_id = c.memory_id
		LEFT JOIN memory_verification_state vs
		  ON vs.memory_space = v.memory_space
		 AND vs.memory_id = v.memory_id
		LEFT JOIN memory_nodes mn
		  ON v.memory_space = 'shared'
		 AND mn.memory_id = v.memory_id
		LEFT JOIN peer_policies pp
		  ON v.memory_space = 'shared'
		 AND pp.peer_id = v.origin_peer_id
		WHERE COALESCE(vs.signature_status, '') != ?
	`
	args = append(args, SignatureStatusInvalidSignature)
	if len(req.Namespaces) > 0 {
		placeholders := make([]string, 0, len(req.Namespaces))
		for _, ns := range req.Namespaces {
			placeholders = append(placeholders, "?")
			args = append(args, ns)
		}
		// #nosec G202 -- placeholders are generated internally for namespace binding.
		query += fmt.Sprintf(" AND v.namespace IN (%s)", strings.Join(placeholders, ",")) // #nosec G202 -- placeholders are generated internally for namespace binding.
	}
	query += `
		ORDER BY
			` + rankingBucketCase("v.memory_space", "vs.signature_status", "mn.author_signature") + `,
			COALESCE(pp.trust_weight, 1.0) DESC,
			c.distance,
			v.authored_at_ms DESC
		LIMIT ?
	`
	args = append(args, limit)

	return s.scanRecallRows(ctx, query, args...)
}

func (s *Service) recallWithFTS(ctx context.Context, req RecallRequest, limit int) ([]RecallResult, error) {
	query := `
		SELECT
			v.memory_space,
			v.memory_id,
			v.namespace,
			v.memory_type,
			v.subject,
			v.body,
			v.lifecycle_state,
			v.authored_at_ms,
			v.source_uri,
			v.source_hash,
			v.origin_peer_id
		FROM recall_memory_view v
		JOIN memory_fts f
		  ON f.memory_space = v.memory_space
		 AND f.memory_id = v.memory_id
		LEFT JOIN memory_verification_state vs
		  ON vs.memory_space = v.memory_space
		 AND vs.memory_id = v.memory_id
		LEFT JOIN memory_nodes mn
		  ON v.memory_space = 'shared'
		 AND mn.memory_id = v.memory_id
		LEFT JOIN peer_policies pp
		  ON v.memory_space = 'shared'
		 AND pp.peer_id = v.origin_peer_id
		WHERE memory_fts MATCH ?
		  AND COALESCE(vs.signature_status, '') != ?
	`
	args := []any{req.Query, SignatureStatusInvalidSignature}

	if !req.IncludePrivate {
		query += ` AND v.memory_space = 'shared'`
	}
	if len(req.Namespaces) > 0 {
		var namespaceClause strings.Builder
		namespaceClause.WriteString(" AND v.namespace IN (")
		for _, ns := range req.Namespaces {
			if namespaceClause.Len() > len(" AND v.namespace IN (") {
				namespaceClause.WriteString(",")
			}
			namespaceClause.WriteString("?")
			args = append(args, ns)
		}
		namespaceClause.WriteString(")")
		// #nosec G202 -- namespace clause is built only from internally generated placeholders.
		query += namespaceClause.String()
	}
	query += `
		ORDER BY
			` + rankingBucketCase("v.memory_space", "vs.signature_status", "mn.author_signature") + `,
			COALESCE(pp.trust_weight, 1.0) DESC,
			bm25(memory_fts),
			v.authored_at_ms DESC
		LIMIT ?
	`
	args = append(args, limit)

	return s.scanRecallRows(ctx, query, args...)
}

func (s *Service) scanRecallRows(ctx context.Context, query string, args ...any) ([]RecallResult, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RecallResult
	for rows.Next() {
		var item RecallResult
		if err := rows.Scan(
			&item.MemorySpace,
			&item.MemoryID,
			&item.Namespace,
			&item.MemoryType,
			&item.Subject,
			&item.Body,
			&item.LifecycleState,
			&item.AuthoredAtMS,
			&item.SourceURI,
			&item.SourceHash,
			&item.OriginPeerID,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type recallKey struct {
	MemorySpace string
	MemoryID    string
}

type recallCandidate struct {
	key              recallKey
	semanticRank     int
	lexicalRank      int
	semanticDistance float64
	lexicalBM25      float64
}

type recallCandidateRow struct {
	key recallKey
	RecallResult
	SignatureStatus  string
	HasSignature     bool
	TrustWeight      float64
	SemanticRank     int
	LexicalRank      int
	SemanticDistance float64
	LexicalBM25      float64
}

type recallGraphStat struct {
	EdgeCount        int
	SupportWeight    float64
	ContradictWeight float64
}

type recallArtifactStat struct {
	SpanCount  int
	QuoteCount int
}

type scoredRecallRow struct {
	row         recallCandidateRow
	score       float64
	bucket      int
	trustWeight float64
}

func (s *Service) recallHybrid(ctx context.Context, req RecallRequest, limit int, vectorEnabled bool) ([]RecallResult, error) {
	candidateLimit := limit * 10
	if candidateLimit < 50 {
		candidateLimit = 50
	}
	if candidateLimit < limit {
		candidateLimit = limit
	}

	candidates := make(map[recallKey]*recallCandidate)

	if vectorEnabled {
		vectorCandidates, err := s.collectVectorRecallCandidates(ctx, req, candidateLimit)
		if err != nil {
			return nil, err
		}
		mergeRecallCandidates(candidates, vectorCandidates)
	}

	lexicalCandidates, err := s.collectFTSRecallCandidates(ctx, req, candidateLimit)
	if err != nil {
		return nil, err
	}
	mergeRecallCandidates(candidates, lexicalCandidates)

	if len(candidates) == 0 {
		return []RecallResult{}, nil
	}

	candidateRows := make([]recallCandidate, 0, len(candidates))
	idsBySpace := map[string][]string{}
	for _, candidate := range candidates {
		candidateRows = append(candidateRows, *candidate)
		idsBySpace[candidate.key.MemorySpace] = append(idsBySpace[candidate.key.MemorySpace], candidate.key.MemoryID)
	}

	rows, err := s.loadRecallCandidateRows(ctx, candidateRows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []RecallResult{}, nil
	}

	graphStats, err := s.loadRecallGraphStats(ctx, idsBySpace)
	if err != nil {
		return nil, err
	}
	artifactStats, err := s.loadRecallArtifactStats(ctx, idsBySpace)
	if err != nil {
		return nil, err
	}

	nowMS := time.Now().UnixMilli()
	scored := make([]scoredRecallRow, 0, len(rows))
	for _, row := range rows {
		key := row.key
		graph := graphStats[key]
		artifact := artifactStats[key]
		bucket := rankingBucket(row.MemorySpace, row.SignatureStatus, row.HasSignature)
		score := recallScore(row, graph, artifact, nowMS)
		scored = append(scored, scoredRecallRow{
			row:         row,
			score:       score,
			bucket:      bucket,
			trustWeight: row.TrustWeight,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].bucket != scored[j].bucket {
			return scored[i].bucket < scored[j].bucket
		}
		if scored[i].trustWeight != scored[j].trustWeight {
			return scored[i].trustWeight > scored[j].trustWeight
		}
		if scored[i].row.AuthoredAtMS != scored[j].row.AuthoredAtMS {
			return scored[i].row.AuthoredAtMS > scored[j].row.AuthoredAtMS
		}
		if scored[i].row.MemorySpace != scored[j].row.MemorySpace {
			return scored[i].row.MemorySpace < scored[j].row.MemorySpace
		}
		return scored[i].row.MemoryID < scored[j].row.MemoryID
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}

	out := make([]RecallResult, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.row.RecallResult)
	}
	return out, nil
}

func mergeRecallCandidates(dst map[recallKey]*recallCandidate, src []recallCandidate) {
	for _, candidate := range src {
		if existing, ok := dst[candidate.key]; ok {
			if candidate.semanticRank > 0 && (existing.semanticRank == 0 || candidate.semanticRank < existing.semanticRank) {
				existing.semanticRank = candidate.semanticRank
				existing.semanticDistance = candidate.semanticDistance
			}
			if candidate.lexicalRank > 0 && (existing.lexicalRank == 0 || candidate.lexicalRank < existing.lexicalRank) {
				existing.lexicalRank = candidate.lexicalRank
				existing.lexicalBM25 = candidate.lexicalBM25
			}
			continue
		}
		copy := candidate
		dst[candidate.key] = &copy
	}
}

func (s *Service) collectVectorRecallCandidates(ctx context.Context, req RecallRequest, limit int) ([]recallCandidate, error) {
	vector, err := embedding.FromText(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	vectorJSON, err := json.Marshal(vector)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT c.memory_space, c.memory_id, c.distance
		FROM memory_embedding_vectors c
		JOIN recall_memory_view v
		  ON v.memory_space = c.memory_space
		 AND v.memory_id = c.memory_id
		WHERE c.embedding MATCH vec_f32(?)
		  AND k = ?
	`
	args := []any{string(vectorJSON), limit}
	if !req.IncludePrivate {
		query += ` AND v.memory_space = 'shared'`
	}
	if len(req.Namespaces) > 0 {
		var namespaceClause strings.Builder
		namespaceClause.WriteString(" AND v.namespace IN (")
		for _, ns := range req.Namespaces {
			if namespaceClause.Len() > len(" AND v.namespace IN (") {
				namespaceClause.WriteString(",")
			}
			namespaceClause.WriteString("?")
			args = append(args, ns)
		}
		namespaceClause.WriteString(")")
		query += namespaceClause.String() // #nosec G202 -- namespace clause is built only from internally generated placeholders.
	}
	query += `
		ORDER BY c.distance
	`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]recallCandidate, 0, limit)
	rank := 0
	for rows.Next() {
		rank++
		var item recallCandidate
		item.semanticRank = rank
		if err := rows.Scan(&item.key.MemorySpace, &item.key.MemoryID, &item.semanticDistance); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) collectFTSRecallCandidates(ctx context.Context, req RecallRequest, limit int) ([]recallCandidate, error) {
	query := `
		SELECT memory_space, memory_id, bm25(memory_fts)
		FROM memory_fts
		WHERE memory_fts MATCH ?
	`
	args := []any{req.Query}
	if !req.IncludePrivate {
		query += ` AND memory_space = 'shared'`
	}
	if len(req.Namespaces) > 0 {
		placeholders := make([]string, 0, len(req.Namespaces))
		for _, ns := range req.Namespaces {
			placeholders = append(placeholders, "?")
			args = append(args, ns)
		}
		// #nosec G202 -- placeholders are generated internally for namespace binding.
		query += fmt.Sprintf(" AND namespace IN (%s)", strings.Join(placeholders, ","))
	}
	query += `
		ORDER BY bm25(memory_fts)
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]recallCandidate, 0, limit)
	rank := 0
	for rows.Next() {
		rank++
		var item recallCandidate
		item.lexicalRank = rank
		if err := rows.Scan(&item.key.MemorySpace, &item.key.MemoryID, &item.lexicalBM25); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) loadRecallCandidateRows(ctx context.Context, candidates []recallCandidate) ([]recallCandidateRow, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	cte, args := buildRecallCandidateCTE(candidates)
	// #nosec G202 -- CTE is generated locally from parameter placeholders, not raw user SQL.
	query := cte + `
		SELECT
			v.memory_space,
			v.memory_id,
			v.namespace,
			v.memory_type,
			v.subject,
			v.body,
			v.lifecycle_state,
			v.authored_at_ms,
			v.source_uri,
			v.source_hash,
			v.origin_peer_id,
			COALESCE(vs.signature_status, ''),
			CASE WHEN v.memory_space = 'shared' AND length(COALESCE(mn.author_signature, X'')) > 0 THEN 1 ELSE 0 END,
			COALESCE(pp.trust_weight, 1.0),
			c.semantic_rank,
			c.lexical_rank,
			c.semantic_distance,
			c.lexical_bm25
		FROM candidate c
		JOIN recall_memory_view v
		  ON v.memory_space = c.memory_space
		 AND v.memory_id = c.memory_id
		LEFT JOIN memory_verification_state vs
		  ON vs.memory_space = v.memory_space
		 AND vs.memory_id = v.memory_id
		LEFT JOIN memory_nodes mn
		  ON v.memory_space = 'shared'
		 AND mn.memory_id = v.memory_id
		LEFT JOIN peer_policies pp
		  ON v.memory_space = 'shared'
		 AND pp.peer_id = v.origin_peer_id
		WHERE COALESCE(vs.signature_status, '') != ?
	`
	args = append(args, SignatureStatusInvalidSignature)
	query += `
		ORDER BY v.memory_space, v.memory_id
	`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]recallCandidateRow, 0, len(candidates))
	for rows.Next() {
		var item recallCandidateRow
		var hasSignature int
		if err := rows.Scan(
			&item.RecallResult.MemorySpace,
			&item.RecallResult.MemoryID,
			&item.RecallResult.Namespace,
			&item.RecallResult.MemoryType,
			&item.RecallResult.Subject,
			&item.RecallResult.Body,
			&item.RecallResult.LifecycleState,
			&item.RecallResult.AuthoredAtMS,
			&item.RecallResult.SourceURI,
			&item.RecallResult.SourceHash,
			&item.RecallResult.OriginPeerID,
			&item.SignatureStatus,
			&hasSignature,
			&item.TrustWeight,
			&item.SemanticRank,
			&item.LexicalRank,
			&item.SemanticDistance,
			&item.LexicalBM25,
		); err != nil {
			return nil, err
		}
		item.key = recallKey{MemorySpace: item.MemorySpace, MemoryID: item.MemoryID}
		item.HasSignature = hasSignature == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func buildRecallCandidateCTE(candidates []recallCandidate) (string, []any) {
	var sb strings.Builder
	sb.WriteString(`
		WITH candidate(memory_space, memory_id, semantic_rank, lexical_rank, semantic_distance, lexical_bm25) AS (
			VALUES
	`)
	args := make([]any, 0, len(candidates)*6)
	for i, candidate := range candidates {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?, ?, ?, ?, ?, ?)")
		args = append(args,
			candidate.key.MemorySpace,
			candidate.key.MemoryID,
			candidate.semanticRank,
			candidate.lexicalRank,
			candidate.semanticDistance,
			candidate.lexicalBM25,
		)
	}
	sb.WriteString(`
			)
		`)
	return sb.String(), args
}

func (s *Service) loadRecallGraphStats(ctx context.Context, idsBySpace map[string][]string) (map[recallKey]recallGraphStat, error) {
	out := make(map[recallKey]recallGraphStat)
	for memorySpace, memoryIDs := range idsBySpace {
		if len(memoryIDs) == 0 {
			continue
		}
		stats, err := s.loadRecallGraphStatsForSpace(ctx, memorySpace, memoryIDs)
		if err != nil {
			return nil, err
		}
		for key, stat := range stats {
			out[key] = stat
		}
	}
	return out, nil
}

func (s *Service) loadRecallGraphStatsForSpace(ctx context.Context, memorySpace string, memoryIDs []string) (map[recallKey]recallGraphStat, error) {
	tableName := "memory_edges"
	if memorySpace == string(VisibilityPrivate) {
		tableName = "private_memory_edges"
	}
	placeholders := make([]string, 0, len(memoryIDs))
	args := make([]any, 0, len(memoryIDs)*2)
	for _, memoryID := range memoryIDs {
		placeholders = append(placeholders, "?")
		args = append(args, memoryID)
	}
	args = append(args, append([]any(nil), args...)...)

	// #nosec G201 -- table names are selected from a fixed enum and placeholders are generated internally.
	query := fmt.Sprintf(`
		SELECT memory_id, SUM(edge_count), SUM(support_weight), SUM(contradict_weight)
		FROM (
			SELECT
				from_memory_id AS memory_id,
				COUNT(*) AS edge_count,
				SUM(CASE WHEN COALESCE(relation_type, '') IN ('contradicts', 'contradiction', 'denies') THEN 0 ELSE weight END) AS support_weight,
				SUM(CASE WHEN COALESCE(relation_type, '') IN ('contradicts', 'contradiction', 'denies') THEN weight ELSE 0 END) AS contradict_weight
			FROM %s
			WHERE from_memory_id IN (%s)
			GROUP BY from_memory_id
			UNION ALL
			SELECT
				to_memory_id AS memory_id,
				COUNT(*) AS edge_count,
				SUM(CASE WHEN COALESCE(relation_type, '') IN ('contradicts', 'contradiction', 'denies') THEN 0 ELSE weight END) AS support_weight,
				SUM(CASE WHEN COALESCE(relation_type, '') IN ('contradicts', 'contradiction', 'denies') THEN weight ELSE 0 END) AS contradict_weight
			FROM %s
			WHERE to_memory_id IN (%s)
			GROUP BY to_memory_id
		)
		GROUP BY memory_id
	`, tableName, strings.Join(placeholders, ","), tableName, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[recallKey]recallGraphStat)
	for rows.Next() {
		var memoryID string
		var stat recallGraphStat
		if err := rows.Scan(&memoryID, &stat.EdgeCount, &stat.SupportWeight, &stat.ContradictWeight); err != nil {
			return nil, err
		}
		out[recallKey{MemorySpace: memorySpace, MemoryID: memoryID}] = stat
	}
	return out, rows.Err()
}

func (s *Service) loadRecallArtifactStats(ctx context.Context, idsBySpace map[string][]string) (map[recallKey]recallArtifactStat, error) {
	out := make(map[recallKey]recallArtifactStat)
	for memorySpace, memoryIDs := range idsBySpace {
		if len(memoryIDs) == 0 {
			continue
		}
		stats, err := s.loadRecallArtifactStatsForSpace(ctx, memorySpace, memoryIDs)
		if err != nil {
			return nil, err
		}
		for key, stat := range stats {
			out[key] = stat
		}
	}
	return out, nil
}

func (s *Service) loadRecallArtifactStatsForSpace(ctx context.Context, memorySpace string, memoryIDs []string) (map[recallKey]recallArtifactStat, error) {
	tableName := "artifact_spans"
	if memorySpace == string(VisibilityPrivate) {
		tableName = "private_artifact_spans"
	}
	placeholders := make([]string, 0, len(memoryIDs))
	args := make([]any, 0, len(memoryIDs))
	for _, memoryID := range memoryIDs {
		placeholders = append(placeholders, "?")
		args = append(args, memoryID)
	}
	// #nosec G201 -- table names are selected from a fixed enum and placeholders are generated internally.
	query := fmt.Sprintf(`
		SELECT memory_id, COUNT(*), SUM(CASE WHEN length(COALESCE(quote_hash, X'')) > 0 THEN 1 ELSE 0 END)
		FROM %s
		WHERE memory_id IN (%s)
		GROUP BY memory_id
	`, tableName, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[recallKey]recallArtifactStat)
	for rows.Next() {
		var memoryID string
		var stat recallArtifactStat
		if err := rows.Scan(&memoryID, &stat.SpanCount, &stat.QuoteCount); err != nil {
			return nil, err
		}
		out[recallKey{MemorySpace: memorySpace, MemoryID: memoryID}] = stat
	}
	return out, rows.Err()
}

func recallScore(row recallCandidateRow, graph recallGraphStat, artifact recallArtifactStat, nowMS int64) float64 {
	bucket := rankingBucket(row.MemorySpace, row.SignatureStatus, row.HasSignature)
	score := float64(3-bucket) * 1000.0
	score += row.TrustWeight * 100.0
	score += recallSourceScore(row) * 20.0
	score += recallGraphScore(graph) * 40.0
	score += recallArtifactScore(artifact) * 40.0
	score += recallRecencyScore(row.AuthoredAtMS, nowMS) * 5.0
	return score
}

func recallSourceScore(row recallCandidateRow) float64 {
	score := 0.0
	if row.SemanticRank > 0 {
		score += 0.6 / float64(row.SemanticRank)
	}
	if row.LexicalRank > 0 {
		score += 0.4 / float64(row.LexicalRank)
	}
	return score
}

func recallGraphScore(stat recallGraphStat) float64 {
	score := float64(stat.EdgeCount)*0.15 + stat.SupportWeight*0.20 - stat.ContradictWeight*0.25
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func recallArtifactScore(stat recallArtifactStat) float64 {
	score := float64(stat.SpanCount)*0.25 + float64(stat.QuoteCount)*0.15
	if score > 1 {
		return 1
	}
	return score
}

func recallRecencyScore(authoredAtMS, nowMS int64) float64 {
	if authoredAtMS <= 0 || nowMS <= 0 {
		return 0
	}
	if authoredAtMS > nowMS {
		return 1
	}
	ageHours := float64(nowMS-authoredAtMS) / float64(time.Hour/time.Millisecond)
	return 1 / (1 + ageHours/24.0)
}

func (s *Service) vectorIndexEnabled(ctx context.Context) (bool, error) {
	s.vecOnce.Do(func() {
		var version string
		if err := s.db.QueryRowContext(ctx, `SELECT vec_version()`).Scan(&version); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "no such function") && strings.Contains(msg, "vec_version") {
				s.vecErr = nil
				s.vecOK = false
				return
			}
			s.vecErr = err
			return
		}
		var exists int
		s.vecErr = s.db.QueryRowContext(ctx, `
			SELECT 1 FROM sqlite_master
			WHERE type = 'table' AND name = 'memory_embedding_vectors'
			LIMIT 1
		`).Scan(&exists)
		if s.vecErr == sql.ErrNoRows {
			s.vecErr = nil
			s.vecOK = false
			return
		}
		s.vecOK = s.vecErr == nil
	})
	return s.vecOK, s.vecErr
}

func (s *Service) Supersede(ctx context.Context, oldMemoryID string, req StoreRequest) (string, error) {
	req.Visibility = VisibilityShared
	req, signature, err := s.prepareStore(req)
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	exists, err := memoryExistsTx(ctx, tx, string(VisibilityShared), oldMemoryID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrMemoryNotFound
	}
	if err := s.storeTx(ctx, tx, req, signature); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE memory_nodes
		SET lifecycle_state = 'superseded'
		WHERE memory_id = ?
	`, oldMemoryID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
		VALUES(?, ?, ?, 'supersedes', 1.0, ?, ?)
	`, uuid.NewString(), req.MemoryID, oldMemoryID, req.OriginPeerID, req.AuthoredAtMS); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return req.MemoryID, nil
}

func (s *Service) Signal(ctx context.Context, req SignalRequest) (string, error) {
	req, err := normalizeSignalRequest(req)
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	exists, err := memoryExistsTx(ctx, tx, req.MemorySpace, req.MemoryID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrMemoryNotFound
	}

	signalID := uuid.NewString()
	switch req.MemorySpace {
	case string(VisibilityShared):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		`, signalID, req.MemoryID, req.OriginPeerID, req.AuthorAgentID, req.SignalType, req.Value, req.Reason, req.AuthoredAtMS); err != nil {
			return "", err
		}
	case string(VisibilityPrivate):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_memory_signals(signal_id, memory_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, ?, ?, ?)
		`, signalID, req.MemoryID, req.AuthorAgentID, req.SignalType, req.Value, req.Reason, req.AuthoredAtMS); err != nil {
			return "", err
		}
	default:
		return "", errors.New("memory_space must be shared or private")
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return signalID, nil
}

func (s *Service) Explain(ctx context.Context, req ExplainRequest) (ExplainResult, error) {
	req.MemorySpace = strings.TrimSpace(req.MemorySpace)
	req.MemoryID = strings.TrimSpace(req.MemoryID)
	req.Query = strings.TrimSpace(req.Query)
	if req.MemorySpace != string(VisibilityShared) && req.MemorySpace != string(VisibilityPrivate) {
		return ExplainResult{}, errors.New("memory_space must be shared or private")
	}
	if req.MemoryID == "" {
		return ExplainResult{}, errors.New("memory_id is required")
	}
	if req.Query == "" {
		return ExplainResult{}, errors.New("query is required")
	}

	record, err := s.loadExplainRecord(ctx, req)
	if err != nil {
		return ExplainResult{}, err
	}

	lexicalBM25, matchedQuery, err := s.lookupLexicalBM25(ctx, req)
	if err != nil {
		return ExplainResult{}, err
	}
	signalSummary, err := s.loadSignalSummary(ctx, req.MemorySpace, req.MemoryID)
	if err != nil {
		return ExplainResult{}, err
	}

	return ExplainResult{
		Provenance: ExplainProvenance{
			Namespace:      record.Namespace,
			MemoryType:     record.MemoryType,
			Subject:        record.Subject,
			LifecycleState: record.LifecycleState,
			SourceURI:      record.SourceURI,
			SourceHash:     record.SourceHash,
			AuthorAgentID:  record.AuthorAgentID,
			OriginPeerID:   record.OriginPeerID,
			AuthoredAtMS:   record.AuthoredAtMS,
		},
		ScoreBreakdown: ExplainScoreBreakdown{
			MatchedQuery:   matchedQuery,
			RecallEligible: record.SignatureStatus != string(SignatureStatusInvalidSignature),
			LexicalBM25:    lexicalBM25,
			RankingBucket:  rankingBucket(record.MemorySpace, record.SignatureStatus, record.HasSignature),
			TrustWeight:    record.PeerTrustWeight,
			AuthoredAtMS:   record.AuthoredAtMS,
		},
		TrustSummary: ExplainTrustSummary{
			SignatureStatus: record.SignatureStatus,
			SignatureDetail: record.SignatureDetail,
			PeerTrustState:  record.PeerTrustState,
			PeerTrustWeight: record.PeerTrustWeight,
			HasSigningKey:   record.HasSigningKey,
		},
		SignalSummary: signalSummary,
	}, nil
}

func (s *Service) TraceDecision(ctx context.Context, req TraceDecisionRequest) (TraceDecisionResult, error) {
	req.MemorySpace = strings.TrimSpace(req.MemorySpace)
	req.MemoryID = strings.TrimSpace(req.MemoryID)
	if req.MemorySpace != string(VisibilityShared) && req.MemorySpace != string(VisibilityPrivate) {
		return TraceDecisionResult{}, errors.New("memory_space must be shared or private")
	}
	if req.MemoryID == "" {
		return TraceDecisionResult{}, errors.New("memory_id is required")
	}
	if req.Depth <= 0 {
		req.Depth = 1
	}

	decision, err := s.loadTraceDecisionNode(ctx, req.MemorySpace, req.MemoryID)
	if err != nil {
		return TraceDecisionResult{}, err
	}

	supports, contradictions, visited, err := s.walkTraceDecision(ctx, req)
	if err != nil {
		return TraceDecisionResult{}, err
	}
	visited[req.MemoryID] = struct{}{}

	artifacts, err := s.loadTraceArtifacts(ctx, req.MemorySpace, visited)
	if err != nil {
		return TraceDecisionResult{}, err
	}

	return TraceDecisionResult{
		Decision:       decision,
		Supports:       supports,
		Contradictions: contradictions,
		Artifacts:      artifacts,
	}, nil
}

func (s *Service) prepareStore(req StoreRequest) (StoreRequest, []byte, error) {
	req, err := normalizeStoreRequest(req)
	if err != nil {
		return StoreRequest{}, nil, err
	}
	signature, err := s.sign(req)
	if err != nil {
		return StoreRequest{}, nil, err
	}
	return req, signature, nil
}

func normalizeStoreRequest(req StoreRequest) (StoreRequest, error) {
	if strings.TrimSpace(req.Body) == "" {
		return StoreRequest{}, errors.New("body is required")
	}
	if req.Visibility != VisibilityShared && req.Visibility != VisibilityPrivate {
		return StoreRequest{}, errors.New("visibility must be shared or private")
	}
	if strings.TrimSpace(req.Namespace) == "" {
		return StoreRequest{}, errors.New("namespace is required")
	}
	if req.MemoryID == "" {
		req.MemoryID = uuid.NewString()
	}
	if req.AuthoredAtMS == 0 {
		req.AuthoredAtMS = time.Now().UnixMilli()
	}
	if req.Scope == "" {
		req.Scope = "team"
	}
	if req.MemoryType == "" {
		req.MemoryType = "fact"
	}
	if req.AuthorAgentID == "" {
		req.AuthorAgentID = "agent/default"
	}
	if req.OriginPeerID == "" {
		req.OriginPeerID = "peer/local"
	}
	for i := range req.ArtifactSpans {
		if err := normalizeArtifactSpanInput(&req.ArtifactSpans[i]); err != nil {
			return StoreRequest{}, err
		}
	}
	for i := range req.Relations {
		if err := normalizeMemoryRelationInput(&req.Relations[i]); err != nil {
			return StoreRequest{}, err
		}
	}
	return req, nil
}

func normalizeArtifactSpanInput(span *ArtifactSpanInput) error {
	span.ArtifactID = strings.TrimSpace(span.ArtifactID)
	span.URI = strings.TrimSpace(span.URI)
	span.ContentHash = strings.TrimSpace(span.ContentHash)
	span.Title = strings.TrimSpace(span.Title)
	span.MimeType = strings.TrimSpace(span.MimeType)
	span.QuoteHash = strings.TrimSpace(span.QuoteHash)
	if span.ArtifactID == "" && span.URI == "" {
		return errors.New("artifact_spans[].uri is required when artifact_id is empty")
	}
	if span.StartOffset < 0 || span.EndOffset < 0 || span.StartLine < 0 || span.EndLine < 0 {
		return errors.New("artifact_spans offsets and lines must be greater than or equal to 0")
	}
	if span.EndOffset > 0 && span.EndOffset < span.StartOffset {
		return errors.New("artifact_spans end_offset must be greater than or equal to start_offset")
	}
	if span.EndLine > 0 && span.EndLine < span.StartLine {
		return errors.New("artifact_spans end_line must be greater than or equal to start_line")
	}
	return nil
}

func normalizeMemoryRelationInput(rel *MemoryRelationInput) error {
	rel.RelationType = strings.TrimSpace(rel.RelationType)
	rel.ToMemoryID = strings.TrimSpace(rel.ToMemoryID)
	if rel.RelationType == "" {
		return errors.New("relations[].relation_type is required")
	}
	if !isAllowedStoreRelationType(rel.RelationType) {
		return errors.New("relations[].relation_type must be supports, contradicts, derived_from, about, caused_by, or references")
	}
	if rel.ToMemoryID == "" {
		return errors.New("relations[].to_memory_id is required")
	}
	if rel.Weight < 0 {
		return errors.New("relations[].weight must be greater than or equal to 0")
	}
	if rel.Weight == 0 {
		rel.Weight = 1.0
	}
	return nil
}

func isAllowedStoreRelationType(relationType string) bool {
	switch strings.TrimSpace(relationType) {
	case "supports", "contradicts", "derived_from", "about", "caused_by", "references":
		return true
	default:
		return false
	}
}

func normalizeSignalRequest(req SignalRequest) (SignalRequest, error) {
	req.MemorySpace = strings.TrimSpace(req.MemorySpace)
	req.MemoryID = strings.TrimSpace(req.MemoryID)
	req.SignalType = strings.TrimSpace(req.SignalType)
	if req.MemorySpace != string(VisibilityShared) && req.MemorySpace != string(VisibilityPrivate) {
		return SignalRequest{}, errors.New("memory_space must be shared or private")
	}
	if req.MemoryID == "" {
		return SignalRequest{}, errors.New("memory_id is required")
	}
	if !isAllowedSignalType(req.SignalType) {
		return SignalRequest{}, errors.New("signal_type must be reinforce, deprecate, confirm, deny, pin, or bookmark")
	}
	if req.Value <= 0 {
		return SignalRequest{}, errors.New("value must be greater than 0")
	}
	if req.AuthoredAtMS == 0 {
		req.AuthoredAtMS = time.Now().UnixMilli()
	}
	if req.AuthorAgentID == "" {
		req.AuthorAgentID = "agent/default"
	}
	if req.MemorySpace == string(VisibilityShared) && req.OriginPeerID == "" {
		req.OriginPeerID = "peer/local"
	}
	return req, nil
}

func isAllowedSignalType(signalType string) bool {
	switch SignalType(signalType) {
	case SignalTypeReinforce, SignalTypeDeprecate, SignalTypeConfirm, SignalTypeDeny, SignalTypePin, SignalTypeBookmark:
		return true
	default:
		return false
	}
}

func (s *Service) storeTx(ctx context.Context, tx *sql.Tx, req StoreRequest, signature []byte) error {
	switch req.Visibility {
	case VisibilityShared:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_nodes(
				memory_id, memory_type, namespace, scope, subject, body, source_uri, source_hash,
				author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
				lifecycle_state, schema_version, author_signature
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 'active', 1, ?)
		`, req.MemoryID, req.MemoryType, req.Namespace, req.Scope, req.Subject, req.Body, req.SourceURI,
			req.SourceHash, req.AuthorAgentID, req.OriginPeerID, req.AuthoredAtMS, signature); err != nil {
			return err
		}
	case VisibilityPrivate:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_memory_nodes(
				memory_id, local_namespace, memory_type, subject, body, source_uri, source_hash,
				author_agent_id, origin_peer_id, authored_at_ms, valid_from_ms, valid_to_ms,
				lifecycle_state, schema_version, author_signature
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 'active', 1, ?)
		`, req.MemoryID, req.Namespace, req.MemoryType, req.Subject, req.Body, req.SourceURI,
			req.SourceHash, req.AuthorAgentID, req.OriginPeerID, req.AuthoredAtMS, signature); err != nil {
			return err
		}
	default:
		return errors.New("visibility must be shared or private")
	}

	if err := upsertVerificationState(ctx, tx, string(req.Visibility), req.MemoryID, SignatureStatusValid, ""); err != nil {
		return err
	}
	if err := insertInitialSignal(ctx, tx, req); err != nil {
		return err
	}
	if err := insertArtifactSpans(ctx, tx, req); err != nil {
		return err
	}
	if err := insertRelations(ctx, tx, req); err != nil {
		return err
	}
	if err := s.upsertRetrievalUnitForStore(ctx, tx, req); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO index_queue(queue_id, memory_space, memory_id, enqueued_at_ms)
		VALUES(?, ?, ?, ?)
	`, uuid.NewString(), req.Visibility, req.MemoryID, time.Now().UnixMilli()); err != nil {
		return err
	}
	return nil
}

func insertInitialSignal(ctx context.Context, tx *sql.Tx, req StoreRequest) error {
	if req.Visibility == VisibilityShared {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO memory_signals(signal_id, memory_id, peer_id, agent_id, signal_type, value, reason, authored_at_ms)
			VALUES(?, ?, ?, ?, 'store', 1.0, 'initial write', ?)
		`, uuid.NewString(), req.MemoryID, req.OriginPeerID, req.AuthorAgentID, req.AuthoredAtMS)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO private_memory_signals(signal_id, memory_id, agent_id, signal_type, value, reason, authored_at_ms)
		VALUES(?, ?, ?, 'store', 1.0, 'initial write', ?)
	`, uuid.NewString(), req.MemoryID, req.AuthorAgentID, req.AuthoredAtMS)
	return err
}

func insertArtifactSpans(ctx context.Context, tx *sql.Tx, req StoreRequest) error {
	if len(req.ArtifactSpans) == 0 {
		return nil
	}
	for _, span := range req.ArtifactSpans {
		artifactID := span.ArtifactID
		if artifactID == "" {
			artifactID = uuid.NewString()
		}
		switch req.Visibility {
		case VisibilityShared:
			if span.URI != "" {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO artifact_refs(artifact_id, namespace, uri, content_hash, title, mime_type, origin_peer_id, authored_at_ms)
					VALUES(?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(artifact_id) DO UPDATE SET
						namespace = excluded.namespace,
						uri = excluded.uri,
						content_hash = excluded.content_hash,
						title = excluded.title,
						mime_type = excluded.mime_type,
						origin_peer_id = excluded.origin_peer_id,
						authored_at_ms = excluded.authored_at_ms
				`, artifactID, req.Namespace, span.URI, span.ContentHash, span.Title, span.MimeType, req.OriginPeerID, req.AuthoredAtMS); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO artifact_spans(
					span_id, artifact_id, memory_id, start_offset, end_offset, start_line, end_line, quote_hash, authored_at_ms
				) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, uuid.NewString(), artifactID, req.MemoryID, span.StartOffset, span.EndOffset, span.StartLine, span.EndLine, span.QuoteHash, req.AuthoredAtMS); err != nil {
				return err
			}
		case VisibilityPrivate:
			if span.URI != "" {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO private_artifact_refs(artifact_id, local_namespace, uri, content_hash, title, mime_type, authored_at_ms)
					VALUES(?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(artifact_id) DO UPDATE SET
						local_namespace = excluded.local_namespace,
						uri = excluded.uri,
						content_hash = excluded.content_hash,
						title = excluded.title,
						mime_type = excluded.mime_type,
						authored_at_ms = excluded.authored_at_ms
				`, artifactID, req.Namespace, span.URI, span.ContentHash, span.Title, span.MimeType, req.AuthoredAtMS); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO private_artifact_spans(
					span_id, artifact_id, memory_id, start_offset, end_offset, start_line, end_line, quote_hash, authored_at_ms
				) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, uuid.NewString(), artifactID, req.MemoryID, span.StartOffset, span.EndOffset, span.StartLine, span.EndLine, span.QuoteHash, req.AuthoredAtMS); err != nil {
				return err
			}
		default:
			return errors.New("visibility must be shared or private")
		}
	}
	return nil
}

func insertRelations(ctx context.Context, tx *sql.Tx, req StoreRequest) error {
	if len(req.Relations) == 0 {
		return nil
	}

	for _, rel := range req.Relations {
		if rel.ToMemoryID == req.MemoryID {
			return errors.New("relations[].to_memory_id cannot reference the stored memory itself")
		}
		exists, err := memoryExistsTx(ctx, tx, string(req.Visibility), rel.ToMemoryID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("relations[].to_memory_id %q not found: %w", rel.ToMemoryID, ErrMemoryNotFound)
		}

		switch req.Visibility {
		case VisibilityShared:
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, origin_peer_id, authored_at_ms)
				VALUES(?, ?, ?, ?, ?, ?, ?)
			`, uuid.NewString(), req.MemoryID, rel.ToMemoryID, rel.RelationType, rel.Weight, req.OriginPeerID, req.AuthoredAtMS); err != nil {
				return err
			}
		case VisibilityPrivate:
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO private_memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, authored_at_ms)
				VALUES(?, ?, ?, ?, ?, ?)
			`, uuid.NewString(), req.MemoryID, rel.ToMemoryID, rel.RelationType, rel.Weight, req.AuthoredAtMS); err != nil {
				return err
			}
		default:
			return errors.New("visibility must be shared or private")
		}
	}
	return nil
}

type explainRecord struct {
	MemorySpace     string
	Namespace       string
	MemoryType      string
	Subject         string
	LifecycleState  string
	SourceURI       string
	SourceHash      string
	AuthorAgentID   string
	OriginPeerID    string
	AuthoredAtMS    int64
	HasSignature    bool
	SignatureStatus string
	SignatureDetail string
	PeerTrustState  string
	PeerTrustWeight float64
	HasSigningKey   bool
}

func (s *Service) loadExplainRecord(ctx context.Context, req ExplainRequest) (explainRecord, error) {
	var row *sql.Row
	switch req.MemorySpace {
	case string(VisibilityShared):
		row = s.db.QueryRowContext(ctx, `
			SELECT
				mn.namespace,
				mn.memory_type,
				mn.subject,
				mn.lifecycle_state,
				mn.source_uri,
				mn.source_hash,
				mn.author_agent_id,
				mn.origin_peer_id,
				mn.authored_at_ms,
				CASE WHEN length(COALESCE(mn.author_signature, X'')) > 0 THEN 1 ELSE 0 END,
				COALESCE(vs.signature_status, ''),
				COALESCE(vs.detail, ''),
				COALESCE(pp.trust_state, ''),
				COALESCE(pp.trust_weight, 1.0),
				CASE WHEN length(COALESCE(pp.signing_public_key, '')) > 0 THEN 1 ELSE 0 END
			FROM memory_nodes mn
			LEFT JOIN memory_verification_state vs
			  ON vs.memory_space = 'shared'
			 AND vs.memory_id = mn.memory_id
			LEFT JOIN peer_policies pp
			  ON pp.peer_id = mn.origin_peer_id
			WHERE mn.memory_id = ?
		`, req.MemoryID)
	case string(VisibilityPrivate):
		row = s.db.QueryRowContext(ctx, `
			SELECT
				pmn.local_namespace,
				pmn.memory_type,
				pmn.subject,
				pmn.lifecycle_state,
				pmn.source_uri,
				pmn.source_hash,
				pmn.author_agent_id,
				pmn.origin_peer_id,
				pmn.authored_at_ms,
				CASE WHEN length(COALESCE(pmn.author_signature, X'')) > 0 THEN 1 ELSE 0 END,
				COALESCE(vs.signature_status, ''),
				COALESCE(vs.detail, ''),
				COALESCE(pp.trust_state, ''),
				COALESCE(pp.trust_weight, 1.0),
				CASE WHEN length(COALESCE(pp.signing_public_key, '')) > 0 THEN 1 ELSE 0 END
			FROM private_memory_nodes pmn
			LEFT JOIN memory_verification_state vs
			  ON vs.memory_space = 'private'
			 AND vs.memory_id = pmn.memory_id
			LEFT JOIN peer_policies pp
			  ON pp.peer_id = pmn.origin_peer_id
			WHERE pmn.memory_id = ?
		`, req.MemoryID)
	default:
		return explainRecord{}, errors.New("memory_space must be shared or private")
	}

	record := explainRecord{
		MemorySpace:     req.MemorySpace,
		PeerTrustWeight: 1.0,
	}
	var hasSignature int
	var hasSigningKey int
	err := row.Scan(
		&record.Namespace,
		&record.MemoryType,
		&record.Subject,
		&record.LifecycleState,
		&record.SourceURI,
		&record.SourceHash,
		&record.AuthorAgentID,
		&record.OriginPeerID,
		&record.AuthoredAtMS,
		&hasSignature,
		&record.SignatureStatus,
		&record.SignatureDetail,
		&record.PeerTrustState,
		&record.PeerTrustWeight,
		&hasSigningKey,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return explainRecord{}, ErrMemoryNotFound
		}
		return explainRecord{}, err
	}
	record.HasSignature = hasSignature == 1
	record.HasSigningKey = hasSigningKey == 1
	return record, nil
}

func (s *Service) lookupLexicalBM25(ctx context.Context, req ExplainRequest) (float64, bool, error) {
	ftsEnabled, err := s.memoryFTSEnabled(ctx)
	if err != nil {
		return 0, false, err
	}
	if ftsEnabled {
		var lexicalBM25 float64
		err := s.db.QueryRowContext(ctx, `
			SELECT bm25(memory_fts_index)
			FROM memory_fts_index
			WHERE memory_fts_index MATCH ?
			  AND memory_space = ?
			  AND memory_id = ?
			LIMIT 1
		`, req.Query, req.MemorySpace, req.MemoryID).Scan(&lexicalBM25)
		if err != nil {
			if err == sql.ErrNoRows {
				return 0, false, nil
			}
			return 0, false, err
		}
		return lexicalBM25, true, nil
	}

	var matched int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memory_fts
		WHERE (lower(subject) LIKE ? OR lower(body) LIKE ?)
		  AND memory_space = ?
		  AND memory_id = ?
		LIMIT 1
	`, "%"+strings.ToLower(req.Query)+"%", "%"+strings.ToLower(req.Query)+"%", req.MemorySpace, req.MemoryID).Scan(&matched)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	if matched == 0 {
		return 0, false, nil
	}
	return 1, true, nil
}

func (s *Service) memoryFTSEnabled(ctx context.Context) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'memory_fts_index' LIMIT 1
	`).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) loadSignalSummary(ctx context.Context, memorySpace, memoryID string) (map[string]ExplainSignalSummary, error) {
	tableName, err := signalTableName(memorySpace)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT signal_type, COUNT(*), COALESCE(SUM(value), 0), COALESCE(MAX(authored_at_ms), 0)
		FROM %s
		WHERE memory_id = ?
		GROUP BY signal_type
		ORDER BY signal_type
	`, tableName), memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]ExplainSignalSummary)
	for rows.Next() {
		var signalType string
		var item ExplainSignalSummary
		if err := rows.Scan(&signalType, &item.Count, &item.Sum, &item.LatestSignalAtMS); err != nil {
			return nil, err
		}
		out[signalType] = item
	}
	return out, rows.Err()
}

func signalTableName(memorySpace string) (string, error) {
	switch memorySpace {
	case string(VisibilityShared):
		return "memory_signals", nil
	case string(VisibilityPrivate):
		return "private_memory_signals", nil
	default:
		return "", errors.New("memory_space must be shared or private")
	}
}

func artifactSpanTableName(memorySpace string) (string, string, string, error) {
	switch memorySpace {
	case string(VisibilityShared):
		return "artifact_spans", "artifact_refs", "namespace", nil
	case string(VisibilityPrivate):
		return "private_artifact_spans", "private_artifact_refs", "local_namespace", nil
	default:
		return "", "", "", errors.New("memory_space must be shared or private")
	}
}

func memoryExistsTx(ctx context.Context, tx *sql.Tx, memorySpace, memoryID string) (bool, error) {
	tableName := ""
	switch memorySpace {
	case string(VisibilityShared):
		tableName = "memory_nodes"
	case string(VisibilityPrivate):
		tableName = "private_memory_nodes"
	default:
		return false, errors.New("memory_space must be shared or private")
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT EXISTS(SELECT 1 FROM %s WHERE memory_id = ?)
	`, tableName), memoryID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func rankingBucket(memorySpace, signatureStatus string, hasSignature bool) int {
	if memorySpace == string(VisibilityPrivate) {
		return 0
	}
	switch signatureStatus {
	case string(SignatureStatusValid):
		return 0
	case string(SignatureStatusMissingSignature):
		return 1
	case string(SignatureStatusUnknownPeer):
		return 2
	default:
		if !hasSignature {
			return 1
		}
		return 1
	}
}

func (s *Service) loadTraceDecisionNode(ctx context.Context, memorySpace, memoryID string) (TraceDecisionNode, error) {
	tableName := "memory_nodes"
	namespaceColumn := "namespace"
	if memorySpace == string(VisibilityPrivate) {
		tableName = "private_memory_nodes"
		namespaceColumn = "local_namespace"
	}

	var node TraceDecisionNode
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT
			?,
			memory_id,
			%s,
			memory_type,
			subject,
			body,
			lifecycle_state,
			source_uri,
			source_hash,
			origin_peer_id,
			authored_at_ms
		FROM %s
		WHERE memory_id = ?
	`, namespaceColumn, tableName), memorySpace, memoryID).Scan(
		&node.MemorySpace,
		&node.MemoryID,
		&node.Namespace,
		&node.MemoryType,
		&node.Subject,
		&node.Body,
		&node.LifecycleState,
		&node.SourceURI,
		&node.SourceHash,
		&node.OriginPeerID,
		&node.AuthoredAtMS,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return TraceDecisionNode{}, ErrMemoryNotFound
		}
		return TraceDecisionNode{}, err
	}
	return node, nil
}

func (s *Service) walkTraceDecision(ctx context.Context, req TraceDecisionRequest) ([]TraceDecisionHop, []TraceDecisionHop, map[string]struct{}, error) {
	supports := []TraceDecisionHop{}
	contradictions := []TraceDecisionHop{}
	visited := map[string]struct{}{req.MemoryID: {}}
	frontier := []string{req.MemoryID}

	for depth := 1; depth <= req.Depth && len(frontier) > 0; depth++ {
		edges, err := s.loadTraceEdges(ctx, req.MemorySpace, frontier)
		if err != nil {
			return nil, nil, nil, err
		}
		nextFrontier := []string{}
		for _, edge := range edges {
			node, err := s.loadTraceDecisionNode(ctx, req.MemorySpace, edge.ToMemoryID)
			if err != nil {
				if errors.Is(err, ErrMemoryNotFound) {
					continue
				}
				return nil, nil, nil, err
			}
			hop := TraceDecisionHop{
				RelationType: edge.RelationType,
				Depth:        depth,
				Memory:       node,
			}
			if isContradictionRelation(edge.RelationType) {
				contradictions = append(contradictions, hop)
			} else {
				supports = append(supports, hop)
			}
			if _, ok := visited[node.MemoryID]; ok {
				continue
			}
			visited[node.MemoryID] = struct{}{}
			nextFrontier = append(nextFrontier, node.MemoryID)
		}
		frontier = nextFrontier
	}
	return supports, contradictions, visited, nil
}

type traceEdge struct {
	RelationType string
	ToMemoryID   string
}

func (s *Service) loadTraceEdges(ctx context.Context, memorySpace string, fromMemoryIDs []string) ([]traceEdge, error) {
	if len(fromMemoryIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(fromMemoryIDs))
	args := make([]any, 0, len(fromMemoryIDs))
	for _, memoryID := range fromMemoryIDs {
		placeholders = append(placeholders, "?")
		args = append(args, memoryID)
	}
	// #nosec G201 -- placeholders are generated internally for trace expansion.
	query := fmt.Sprintf(`
			SELECT e.relation_type, e.to_memory_id
		FROM memory_edges e
		LEFT JOIN local_graph_suspensions s
		  ON s.entity_type = 'memory_edge'
		 AND s.entity_id = e.edge_id
		 AND s.resolved_at_ms = 0
		WHERE e.from_memory_id IN (%s)
		  AND s.entity_id IS NULL
		ORDER BY e.authored_at_ms, e.edge_id
	`, strings.Join(placeholders, ","))
	if memorySpace == string(VisibilityPrivate) {
		// #nosec G201 -- placeholders are generated internally for trace expansion.
		query = fmt.Sprintf(`
				SELECT relation_type, to_memory_id
			FROM private_memory_edges
			WHERE from_memory_id IN (%s)
			ORDER BY authored_at_ms, edge_id
		`, strings.Join(placeholders, ","))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []traceEdge{}
	for rows.Next() {
		var edge traceEdge
		if err := rows.Scan(&edge.RelationType, &edge.ToMemoryID); err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

func isContradictionRelation(relationType string) bool {
	switch strings.TrimSpace(relationType) {
	case "contradicts", "contradiction", "denies":
		return true
	default:
		return false
	}
}

func (s *Service) loadTraceArtifacts(ctx context.Context, memorySpace string, memoryIDs map[string]struct{}) ([]TraceDecisionArtifact, error) {
	if len(memoryIDs) == 0 {
		return nil, nil
	}
	spanTable, refTable, namespaceColumn, err := artifactSpanTableName(memorySpace)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, 0, len(memoryIDs))
	args := make([]any, 0, len(memoryIDs))
	for memoryID := range memoryIDs {
		placeholders = append(placeholders, "?")
		args = append(args, memoryID)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			s.artifact_id,
			s.memory_id,
			r.uri,
			r.title,
			r.mime_type,
			s.start_offset,
			s.end_offset,
			s.start_line,
			s.end_line,
			s.quote_hash
		FROM %s s
		JOIN %s r ON r.artifact_id = s.artifact_id
		WHERE s.memory_id IN (%s)
		ORDER BY r.%s, s.authored_at_ms, s.span_id
	`, spanTable, refTable, strings.Join(placeholders, ","), namespaceColumn), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TraceDecisionArtifact{}
	for rows.Next() {
		var item TraceDecisionArtifact
		if err := rows.Scan(
			&item.ArtifactID,
			&item.MemoryID,
			&item.URI,
			&item.Title,
			&item.MimeType,
			&item.StartOffset,
			&item.EndOffset,
			&item.StartLine,
			&item.EndLine,
			&item.QuoteHash,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func rankingBucketCase(memorySpaceExpr, signatureStatusExpr, signatureExpr string) string {
	return fmt.Sprintf(`CASE
			WHEN %s = 'private' THEN 0
			WHEN COALESCE(%s, '') = 'valid' THEN 0
			WHEN COALESCE(%s, '') = 'missing_signature'
				OR length(COALESCE(%s, X'')) = 0 THEN 1
			WHEN COALESCE(%s, '') = 'unknown_peer' THEN 2
			ELSE 1
		END`, memorySpaceExpr, signatureStatusExpr, signatureStatusExpr, signatureExpr, signatureStatusExpr)
}

func (s *Service) sign(req StoreRequest) ([]byte, error) {
	if s.signer == nil {
		return nil, errors.New("signer is required")
	}
	return s.signer.SignClaim(signing.ClaimPayload{
		MemoryID:       req.MemoryID,
		MemoryType:     req.MemoryType,
		Namespace:      req.Namespace,
		Subject:        req.Subject,
		Body:           req.Body,
		SourceURI:      req.SourceURI,
		SourceHash:     req.SourceHash,
		AuthorAgentID:  req.AuthorAgentID,
		OriginPeerID:   req.OriginPeerID,
		AuthoredAtMS:   req.AuthoredAtMS,
		ValidFromMS:    0,
		ValidToMS:      0,
		PayloadVersion: signing.PayloadVersion,
	})
}

func upsertVerificationState(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, memorySpace, memoryID string, status SignatureStatus, detail string) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO memory_verification_state(memory_space, memory_id, signature_status, detail, checked_at_ms)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(memory_space, memory_id) DO UPDATE SET
			signature_status = excluded.signature_status,
			detail = excluded.detail,
			checked_at_ms = excluded.checked_at_ms
	`, memorySpace, memoryID, status, detail, time.Now().UnixMilli())
	return err
}
