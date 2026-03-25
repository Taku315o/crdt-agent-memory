package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"crdt-agent-memory/internal/embedding"
)

func normalizeRecallRequest(req RecallRequest) RecallRequest {
	req.Query = strings.TrimSpace(req.Query)
	req.ProjectKey = strings.TrimSpace(req.ProjectKey)
	req.BranchName = strings.TrimSpace(req.BranchName)
	if !req.IncludePrivate && !req.IncludeTranscript {
		req.IncludePrivate = true
	}
	if !req.IncludeShared {
		req.IncludeShared = true
	}
	if !req.IncludePrivate && !req.IncludeShared && !req.IncludeTranscript {
		req.IncludeTranscript = true
	}
	return req
}

func retrievalUnitHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func retrievalSourceTypeForVisibility(v Visibility) string {
	if v == VisibilityPrivate {
		return "private_memory"
	}
	return "shared_memory"
}

func (s *Service) upsertRetrievalUnitForStore(ctx context.Context, tx *sql.Tx, req StoreRequest) error {
	return upsertRetrievalUnit(ctx, tx, retrievalUnitRecord{
		UnitID:         req.MemoryID,
		SourceType:     retrievalSourceTypeForVisibility(req.Visibility),
		SourceID:       req.MemoryID,
		MemorySpace:    string(req.Visibility),
		Namespace:      req.Namespace,
		UnitKind:       req.MemoryType,
		Title:          req.Subject,
		Body:           req.Body,
		AuthoredAtMS:   req.AuthoredAtMS,
		Sensitivity:    "private",
		RetentionClass: "default",
		State:          "active",
		SourceURI:      req.SourceURI,
	})
}

type retrievalUnitRecord struct {
	UnitID         string
	SourceType     string
	SourceID       string
	MemorySpace    string
	Namespace      string
	UnitKind       string
	Title          string
	Body           string
	AuthoredAtMS   int64
	Sensitivity    string
	RetentionClass string
	State          string
	SourceURI      string
	ProjectKey     string
	BranchName     string
}

type transcriptChunkRecord struct {
	ChunkID      string
	SessionID    string
	ChunkKind    string
	Text         string
	AuthoredAtMS int64
}

func upsertRetrievalUnit(ctx context.Context, tx *sql.Tx, unit retrievalUnitRecord) error {
	if unit.AuthoredAtMS == 0 {
		unit.AuthoredAtMS = time.Now().UnixMilli()
	}
	if unit.State == "" {
		unit.State = "active"
	}
	if unit.Sensitivity == "" {
		unit.Sensitivity = "private"
	}
	if unit.RetentionClass == "" {
		unit.RetentionClass = "default"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_units(
			unit_id, source_type, source_id, memory_space, namespace, unit_kind, title, body, body_hash,
			authored_at_ms, sensitivity, retention_class, state, source_uri, project_key, branch_name, schema_version
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(unit_id) DO UPDATE SET
			source_type = excluded.source_type,
			source_id = excluded.source_id,
			memory_space = excluded.memory_space,
			namespace = excluded.namespace,
			unit_kind = excluded.unit_kind,
			title = excluded.title,
			body = excluded.body,
			body_hash = excluded.body_hash,
			authored_at_ms = excluded.authored_at_ms,
			sensitivity = excluded.sensitivity,
			retention_class = excluded.retention_class,
			state = excluded.state,
			source_uri = excluded.source_uri,
			project_key = excluded.project_key,
			branch_name = excluded.branch_name
	`, unit.UnitID, unit.SourceType, unit.SourceID, unit.MemorySpace, unit.Namespace, unit.UnitKind, unit.Title,
		unit.Body, retrievalUnitHash(unit.Body), unit.AuthoredAtMS, unit.Sensitivity, unit.RetentionClass,
		unit.State, unit.SourceURI, unit.ProjectKey, unit.BranchName); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO retrieval_index_queue(queue_id, unit_id, enqueued_at_ms)
		VALUES(?, ?, ?)
	`, uuid.NewString(), unit.UnitID, time.Now().UnixMilli())
	return err
}

func (s *Service) Recall(ctx context.Context, req RecallRequest) ([]RecallResult, error) {
	req = normalizeRecallRequest(req)
	if req.Query == "" {
		return nil, errors.New("query is required")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	return s.recallRetrievalUnits(ctx, req, limit)
}

func (s *Service) recallRetrievalUnits(ctx context.Context, req RecallRequest, limit int) ([]RecallResult, error) {
	candidateLimit := limit * 10
	if candidateLimit < 50 {
		candidateLimit = 50
	}

	candidates := map[string]*recallCandidate{}
	vectorEnabled, err := s.retrievalVectorIndexEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if vectorEnabled {
		vectorRows, err := s.collectRetrievalVectorCandidates(ctx, req, candidateLimit)
		if err != nil {
			return nil, err
		}
		mergeRetrievalCandidates(candidates, vectorRows)
	}
	ftsRows, err := s.collectRetrievalFTSCandidates(ctx, req, candidateLimit)
	if err != nil {
		return nil, err
	}
	mergeRetrievalCandidates(candidates, ftsRows)
	if len(candidates) == 0 {
		return []RecallResult{}, nil
	}

	rows, err := s.loadRetrievalCandidateRows(ctx, candidates)
	if err != nil {
		return nil, err
	}
	idsBySpace := map[string][]string{}
	for _, row := range rows {
		if row.MemorySpace == "shared" || row.MemorySpace == "private" {
			idsBySpace[row.MemorySpace] = append(idsBySpace[row.MemorySpace], row.MemoryID)
		}
	}
	graphStats, err := s.loadRecallGraphStats(ctx, idsBySpace)
	if err != nil {
		return nil, err
	}
	artifactStats, err := s.loadRecallArtifactStats(ctx, idsBySpace)
	if err != nil {
		return nil, err
	}
	return rankRetrievalRows(rows, graphStats, artifactStats, limit), nil
}

func allowedMemorySpaces(req RecallRequest) []string {
	spaces := []string{}
	if req.IncludeTranscript {
		spaces = append(spaces, "transcript")
	}
	if req.IncludePrivate {
		spaces = append(spaces, "private")
	}
	if req.IncludeShared {
		spaces = append(spaces, "shared")
	}
	return spaces
}

func mergeRetrievalCandidates(dst map[string]*recallCandidate, src []recallCandidate) {
	for _, candidate := range src {
		key := candidate.key.MemoryID
		if existing, ok := dst[key]; ok {
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
		dst[key] = &copy
	}
}

func appendInClause(query *strings.Builder, column string, values []string, args []any) []any {
	if len(values) == 0 {
		return args
	}
	query.WriteString(" AND ")
	query.WriteString(column)
	query.WriteString(" IN (")
	for i, value := range values {
		if i > 0 {
			query.WriteString(",")
		}
		query.WriteString("?")
		args = append(args, value)
	}
	query.WriteString(")")
	return args
}

func (s *Service) collectRetrievalFTSCandidates(ctx context.Context, req RecallRequest, limit int) ([]recallCandidate, error) {
	ftsEnabled, err := s.retrievalFTSEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if ftsEnabled {
		return s.collectRetrievalFTS5Candidates(ctx, req, limit)
	}
	return s.collectRetrievalLIKECandidates(ctx, req, limit)
}

func (s *Service) collectRetrievalFTS5Candidates(ctx context.Context, req RecallRequest, limit int) ([]recallCandidate, error) {
	query := strings.Builder{}
	query.WriteString(`
		SELECT unit_id, bm25(retrieval_fts_index)
		FROM retrieval_fts_index
		WHERE retrieval_fts_index MATCH ?
	`)
	args := []any{req.Query}
	args = appendInClause(&query, "memory_space", allowedMemorySpaces(req), args)
	args = appendInClause(&query, "namespace", req.Namespaces, args)
	args = appendInClause(&query, "source_type", req.SourceTypes, args)
	args = appendInClause(&query, "unit_kind", req.UnitKinds, args)
	query.WriteString(latestTranscriptChunkFilter("unit_id"))
	query.WriteString(" ORDER BY bm25(retrieval_fts_index) LIMIT ?")
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recallCandidate
	rank := 0
	for rows.Next() {
		rank++
		var unitID string
		var bm25 float64
		if err := rows.Scan(&unitID, &bm25); err != nil {
			return nil, err
		}
		out = append(out, recallCandidate{
			key:         recallKey{MemoryID: unitID},
			lexicalRank: rank,
			lexicalBM25: bm25,
		})
	}
	return out, rows.Err()
}

func (s *Service) collectRetrievalLIKECandidates(ctx context.Context, req RecallRequest, limit int) ([]recallCandidate, error) {
	query := strings.Builder{}
	query.WriteString(`
		SELECT unit_id, title, body
		FROM retrieval_fts
		WHERE 1 = 1
	`)
	args := []any{}
	args = appendInClause(&query, "memory_space", allowedMemorySpaces(req), args)
	args = appendInClause(&query, "namespace", req.Namespaces, args)
	args = appendInClause(&query, "source_type", req.SourceTypes, args)
	args = appendInClause(&query, "unit_kind", req.UnitKinds, args)
	query.WriteString(latestTranscriptChunkFilter("unit_id"))
	query.WriteString(" ORDER BY unit_id")

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scoredUnit struct {
		unitID string
		score  int
	}
	terms := searchTerms(req.Query)
	scored := make([]scoredUnit, 0)
	for rows.Next() {
		var unitID string
		var title string
		var body string
		if err := rows.Scan(&unitID, &title, &body); err != nil {
			return nil, err
		}
		score := termMatchScore(terms, title+" "+body)
		if score > 0 {
			scored = append(scored, scoredUnit{unitID: unitID, score: score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].unitID < scored[j].unitID
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]recallCandidate, 0, len(scored))
	for i, item := range scored {
		out = append(out, recallCandidate{
			key:         recallKey{MemoryID: item.unitID},
			lexicalRank: i + 1,
			lexicalBM25: float64(item.score),
		})
	}
	return out, nil
}

func searchTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		if len(field) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func termMatchScore(terms []string, text string) int {
	text = strings.ToLower(text)
	score := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			score++
		}
	}
	return score
}

func (s *Service) collectRetrievalVectorCandidates(ctx context.Context, req RecallRequest, limit int) ([]recallCandidate, error) {
	vector, err := embedding.FromText(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	vectorJSON, err := json.Marshal(vector)
	if err != nil {
		return nil, err
	}
	query := strings.Builder{}
	query.WriteString(`
		SELECT rv.unit_id, rv.distance
		FROM retrieval_embedding_vectors rv
		JOIN retrieval_units ru ON ru.unit_id = rv.unit_id
		WHERE rv.embedding MATCH vec_f32(?)
		  AND k = ?
		  AND ru.sensitivity != 'secret'
		  AND ru.state = 'active'
	`)
	args := []any{string(vectorJSON), limit}
	args = appendInClause(&query, "ru.memory_space", allowedMemorySpaces(req), args)
	args = appendInClause(&query, "ru.namespace", req.Namespaces, args)
	args = appendInClause(&query, "ru.source_type", req.SourceTypes, args)
	args = appendInClause(&query, "ru.unit_kind", req.UnitKinds, args)
	query.WriteString(latestTranscriptChunkFilter("ru.unit_id"))
	if req.ProjectKey != "" {
		query.WriteString(" AND ru.project_key = ?")
		args = append(args, req.ProjectKey)
	}
	if req.BranchName != "" {
		query.WriteString(" AND ru.branch_name = ?")
		args = append(args, req.BranchName)
	}
	query.WriteString(" ORDER BY rv.distance")

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recallCandidate
	rank := 0
	for rows.Next() {
		rank++
		var unitID string
		var distance float64
		if err := rows.Scan(&unitID, &distance); err != nil {
			return nil, err
		}
		out = append(out, recallCandidate{
			key:              recallKey{MemoryID: unitID},
			semanticRank:     rank,
			semanticDistance: distance,
		})
	}
	return out, rows.Err()
}

func (s *Service) loadRetrievalCandidateRows(ctx context.Context, candidates map[string]*recallCandidate) ([]recallCandidateRow, error) {
	var sb strings.Builder
	sb.WriteString("WITH candidate(unit_id, semantic_rank, lexical_rank, semantic_distance, lexical_bm25) AS (VALUES")
	args := make([]any, 0, len(candidates)*5)
	i := 0
	for unitID, candidate := range candidates {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?, ?, ?, ?, ?)")
		args = append(args, unitID, candidate.semanticRank, candidate.lexicalRank, candidate.semanticDistance, candidate.lexicalBM25)
		i++
	}
	sb.WriteString(`)
		SELECT
			ru.unit_id,
			ru.source_type,
			ru.memory_space,
			ru.source_id,
			ru.namespace,
			ru.unit_kind,
			ru.title,
			ru.body,
			ru.state,
			ru.authored_at_ms,
			ru.source_uri,
			CASE
				WHEN ru.source_type = 'shared_memory' THEN COALESCE(mn.source_hash, '')
				WHEN ru.source_type = 'private_memory' THEN COALESCE(pmn.source_hash, '')
				ELSE ''
			END AS source_hash,
			CASE
				WHEN ru.source_type = 'shared_memory' THEN COALESCE(mn.origin_peer_id, '')
				WHEN ru.source_type = 'private_memory' THEN COALESCE(pmn.origin_peer_id, '')
				ELSE ''
			END AS origin_peer_id,
			COALESCE(vs.signature_status, ''),
			CASE WHEN ru.memory_space = 'shared' AND length(COALESCE(mn.author_signature, X'')) > 0 THEN 1 ELSE 0 END,
			COALESCE(pp.trust_weight, 1.0),
			c.semantic_rank,
			c.lexical_rank,
			c.semantic_distance,
			c.lexical_bm25
		FROM candidate c
		JOIN retrieval_units ru ON ru.unit_id = c.unit_id
		LEFT JOIN memory_nodes mn
		  ON ru.source_type = 'shared_memory' AND mn.memory_id = ru.source_id
		LEFT JOIN private_memory_nodes pmn
		  ON ru.source_type = 'private_memory' AND pmn.memory_id = ru.source_id
		LEFT JOIN memory_verification_state vs
		  ON vs.memory_space = ru.memory_space
		 AND vs.memory_id = ru.source_id
		 AND ru.source_type IN ('shared_memory', 'private_memory')
		LEFT JOIN peer_policies pp
		  ON ru.memory_space = 'shared'
		 AND pp.peer_id = mn.origin_peer_id
		WHERE ru.state = 'active'
		  AND COALESCE(vs.signature_status, '') != ?
	`)
	sb.WriteString(latestTranscriptChunkFilter("ru.unit_id"))
	sb.WriteString(`
	`)
	args = append(args, SignatureStatusInvalidSignature)
	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recallCandidateRow
	for rows.Next() {
		var item recallCandidateRow
		var unitID string
		var hasSignature int
		if err := rows.Scan(
			&unitID,
			&item.RecallResult.SourceType,
			&item.RecallResult.MemorySpace,
			&item.RecallResult.MemoryID,
			&item.RecallResult.Namespace,
			&item.RecallResult.UnitKind,
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
		item.RecallResult.UnitID = unitID
		item.RecallResult.MemoryType = item.RecallResult.UnitKind
		item.key = recallKey{MemorySpace: item.MemorySpace, MemoryID: item.MemoryID}
		item.HasSignature = hasSignature == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func latestTranscriptChunkFilter(unitRef string) string {
	return fmt.Sprintf(`
	  AND (
		%s NOT IN (
			SELECT unit_id FROM retrieval_units WHERE source_type = 'transcript_chunk'
		)
		OR EXISTS (
			SELECT 1
			FROM transcript_chunks tc
			WHERE tc.chunk_id = %s
			  AND tc.chunk_strategy_version = (
				SELECT MAX(tc2.chunk_strategy_version)
				FROM transcript_chunks tc2
				WHERE tc2.session_id = tc.session_id
			  )
		)
	  )
	`, unitRef, unitRef)
}

func rankRetrievalRows(rows []recallCandidateRow, graphStats map[recallKey]recallGraphStat, artifactStats map[recallKey]recallArtifactStat, limit int) []RecallResult {
	nowMS := time.Now().UnixMilli()
	scored := make([]scoredRecallRow, 0, len(rows))
	for _, row := range rows {
		bucket := rankingBucket(row.MemorySpace, row.SignatureStatus, row.HasSignature)
		score := recallScore(row, graphStats[row.key], artifactStats[row.key], nowMS)
		if row.MemorySpace == "transcript" {
			score += 25
		}
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
		if scored[i].row.AuthoredAtMS != scored[j].row.AuthoredAtMS {
			return scored[i].row.AuthoredAtMS > scored[j].row.AuthoredAtMS
		}
		return scored[i].row.UnitID < scored[j].row.UnitID
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]RecallResult, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.row.RecallResult)
	}
	return out
}

func (s *Service) retrievalVectorIndexEnabled(ctx context.Context) (bool, error) {
	var version string
	if err := s.db.QueryRowContext(ctx, `SELECT vec_version()`).Scan(&version); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no such function") && strings.Contains(msg, "vec_version") {
			return false, nil
		}
		return false, err
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'retrieval_embedding_vectors' LIMIT 1
	`).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil && version != "", err
}

func (s *Service) retrievalFTSEnabled(ctx context.Context) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'retrieval_fts_index' LIMIT 1
	`).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) Promote(ctx context.Context, req PromoteRequest) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	memoryID, err := s.promoteTx(ctx, tx, req)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return memoryID, nil
}

func (s *Service) promoteTx(ctx context.Context, tx *sql.Tx, req PromoteRequest) (string, error) {
	if len(req.ChunkIDs) == 0 {
		return "", errors.New("chunk_ids is required")
	}
	if strings.TrimSpace(req.Namespace) == "" {
		return "", errors.New("namespace is required")
	}
	if req.MemoryType == "" {
		req.MemoryType = "note"
	}
	if req.AuthoredAtMS == 0 {
		req.AuthoredAtMS = time.Now().UnixMilli()
	}
	if req.AuthorAgentID == "" {
		req.AuthorAgentID = "agent/default"
	}
	if req.OriginPeerID == "" {
		req.OriginPeerID = "peer/local"
	}

	bodies := make([]string, 0, len(req.ChunkIDs))
	artifactSpans := make([]ArtifactSpanInput, 0)
	seenArtifacts := map[string]struct{}{}
	chunkRecords := make([]transcriptChunkRecord, 0, len(req.ChunkIDs))
	for _, chunkID := range req.ChunkIDs {
		var chunk transcriptChunkRecord
		if err := tx.QueryRowContext(ctx, `
			SELECT chunk_id, session_id, chunk_kind, text, authored_at_ms
			FROM transcript_chunks
			WHERE chunk_id = ?
		`, chunkID).Scan(&chunk.ChunkID, &chunk.SessionID, &chunk.ChunkKind, &chunk.Text, &chunk.AuthoredAtMS); err != nil {
			if err == sql.ErrNoRows {
				return "", ErrMemoryNotFound
			}
			return "", err
		}
		chunkRecords = append(chunkRecords, chunk)
		bodies = append(bodies, chunk.Text)
		spans, err := loadTranscriptArtifactInputs(ctx, tx, req.Namespace, chunkID)
		if err != nil {
			return "", err
		}
		for _, span := range spans {
			key := span.ArtifactID + "|" + span.QuoteHash + "|" + fmt.Sprintf("%d:%d", span.StartOffset, span.EndOffset)
			if _, ok := seenArtifacts[key]; ok {
				continue
			}
			seenArtifacts[key] = struct{}{}
			artifactSpans = append(artifactSpans, span)
			if req.SourceURI == "" && span.URI != "" {
				req.SourceURI = span.URI
			}
		}
	}
	body := strings.Join(bodies, "\n\n")
	if req.Subject == "" {
		req.Subject = "promoted transcript memory"
	}
	storeReq := StoreRequest{
		Visibility:    VisibilityPrivate,
		Namespace:     req.Namespace,
		MemoryType:    req.MemoryType,
		Subject:       req.Subject,
		Body:          body,
		SourceURI:     req.SourceURI,
		AuthorAgentID: req.AuthorAgentID,
		OriginPeerID:  req.OriginPeerID,
		AuthoredAtMS:  req.AuthoredAtMS,
		ArtifactSpans: artifactSpans,
	}
	storeReq, signature, err := s.prepareStore(storeReq)
	if err != nil {
		return "", err
	}
	if err := s.storeTx(ctx, tx, storeReq, signature); err != nil {
		return "", err
	}
	if err := promoteInferredRelations(ctx, tx, storeReq.MemoryID, req.AuthoredAtMS, chunkRecords); err != nil {
		return "", err
	}
	for _, chunkID := range req.ChunkIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcript_promotions(promotion_id, chunk_id, memory_id, created_at_ms)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(chunk_id, memory_id) DO NOTHING
		`, uuid.NewString(), chunkID, storeReq.MemoryID, req.AuthoredAtMS); err != nil {
			return "", err
		}
	}
	return storeReq.MemoryID, nil
}

func promoteInferredRelations(ctx context.Context, tx *sql.Tx, memoryID string, authoredAtMS int64, chunks []transcriptChunkRecord) (err error) {
	if len(chunks) == 0 {
		return nil
	}
	relationType, ok := inferPromotionRelationType(chunks)
	if !ok {
		return nil
	}
	sessionIDs := map[string]struct{}{}
	for _, chunk := range chunks {
		sessionIDs[chunk.SessionID] = struct{}{}
	}
	for sessionID := range sessionIDs {
		if err := func() (err error) {
			rows, err := tx.QueryContext(ctx, `
				SELECT DISTINCT tp.memory_id
				FROM transcript_promotions tp
				JOIN transcript_chunks tc ON tc.chunk_id = tp.chunk_id
				JOIN private_memory_nodes pmn ON pmn.memory_id = tp.memory_id
				WHERE tc.session_id = ?
				  AND tp.memory_id != ?
				  AND pmn.memory_type = 'decision'
			`, sessionID, memoryID)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := rows.Close(); err == nil {
					err = cerr
				}
			}()
			for rows.Next() {
				var targetID string
				if err := rows.Scan(&targetID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO private_memory_edges(edge_id, from_memory_id, to_memory_id, relation_type, weight, authored_at_ms)
					SELECT ?, ?, ?, ?, 1.0, ?
					WHERE NOT EXISTS (
						SELECT 1 FROM private_memory_edges
						WHERE from_memory_id = ? AND to_memory_id = ? AND relation_type = ?
					)
				`, uuid.NewString(), memoryID, targetID, relationType, authoredAtMS, memoryID, targetID, relationType); err != nil {
					return err
				}
			}
			return rows.Err()
		}(); err != nil {
			return err
		}
	}
	return nil
}

func inferPromotionRelationType(chunks []transcriptChunkRecord) (string, bool) {
	combined := strings.ToLower("")
	hasRationaleLike := false
	hasContradiction := false
	for _, chunk := range chunks {
		combined += "\n" + strings.ToLower(chunk.Text)
		switch chunk.ChunkKind {
		case "rationale", "debug_trace", "task_candidate":
			hasRationaleLike = true
		}
	}
	if strings.Contains(combined, "contradict") || strings.Contains(combined, "instead") || strings.Contains(combined, "やめる") || strings.Contains(combined, "却下") {
		hasContradiction = true
	}
	if hasContradiction {
		return "contradicts", true
	}
	if hasRationaleLike {
		return "supports", true
	}
	return "", false
}

func loadTranscriptArtifactInputs(ctx context.Context, tx *sql.Tx, namespace, chunkID string) ([]ArtifactSpanInput, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			s.artifact_id,
			COALESCE(r.uri, ''),
			COALESCE(r.content_hash, ''),
			COALESCE(r.title, ''),
			COALESCE(r.mime_type, ''),
			s.start_offset,
			s.end_offset,
			s.start_line,
			s.end_line,
			s.quote_hash
		FROM transcript_artifact_spans s
		LEFT JOIN private_artifact_refs r
		  ON r.artifact_id = s.artifact_id
		 AND r.local_namespace = ?
		WHERE s.chunk_id = ?
		ORDER BY s.start_offset, s.end_offset, s.span_id
	`, namespace, chunkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArtifactSpanInput
	for rows.Next() {
		var item ArtifactSpanInput
		if err := rows.Scan(
			&item.ArtifactID,
			&item.URI,
			&item.ContentHash,
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

func (s *Service) Publish(ctx context.Context, req PublishRequest) (string, error) {
	req.PrivateMemoryID = strings.TrimSpace(req.PrivateMemoryID)
	if req.PrivateMemoryID == "" {
		return "", errors.New("private_memory_id is required")
	}
	policy, err := normalizeRedactionPolicy(req.RedactionPolicy)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var row StoreRequest
	if err := tx.QueryRowContext(ctx, `
		SELECT local_namespace, memory_type, subject, body, source_uri, source_hash, author_agent_id, origin_peer_id, authored_at_ms
		FROM private_memory_nodes
		WHERE memory_id = ?
	`, req.PrivateMemoryID).Scan(
		&row.Namespace,
		&row.MemoryType,
		&row.Subject,
		&row.Body,
		&row.SourceURI,
		&row.SourceHash,
		&row.AuthorAgentID,
		&row.OriginPeerID,
		&row.AuthoredAtMS,
	); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrMemoryNotFound
		}
		return "", err
	}
	row.Subject = redactText(row.Subject, policy)
	row.Body = redactText(row.Body, policy)
	row.SourceURI = redactSourceURI(row.SourceURI, policy)
	row.Visibility = VisibilityShared
	row, signature, err := s.prepareStore(row)
	if err != nil {
		return "", err
	}
	if err := s.storeTx(ctx, tx, row, signature); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_publications(publication_id, private_memory_id, shared_memory_id, published_at_ms)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(private_memory_id, shared_memory_id) DO NOTHING
	`, uuid.NewString(), req.PrivateMemoryID, row.MemoryID, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return row.MemoryID, nil
}

func normalizeRedactionPolicy(policy string) (string, error) {
	policy = strings.TrimSpace(strings.ToLower(policy))
	if policy == "" {
		policy = "default"
	}
	switch policy {
	case "default", "strict":
		return policy, nil
	default:
		return "", fmt.Errorf("redaction_policy must be default or strict")
	}
}

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|secret)\b\s*[:=]\s*\S+`)
	bearerPattern           = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._-]+`)
	userPathPattern         = regexp.MustCompile(`/(Users|home)/[^\s"']+`)
	fileURIPattern          = regexp.MustCompile(`file://[^\s"']+`)
)

func redactText(text, policy string) string {
	redacted := secretAssignmentPattern.ReplaceAllString(text, `$1=[REDACTED]`)
	redacted = bearerPattern.ReplaceAllString(redacted, `Bearer [REDACTED]`)
	redacted = fileURIPattern.ReplaceAllString(redacted, `file://[REDACTED_PATH]`)
	redacted = userPathPattern.ReplaceAllString(redacted, `[REDACTED_PATH]`)
	if policy == "strict" {
		redacted = strings.ReplaceAll(redacted, "http://", "")
		redacted = strings.ReplaceAll(redacted, "https://", "")
	}
	return redacted
}

func redactSourceURI(sourceURI, policy string) string {
	sourceURI = strings.TrimSpace(sourceURI)
	if sourceURI == "" {
		return ""
	}
	if strings.HasPrefix(sourceURI, "/") || strings.HasPrefix(strings.ToLower(sourceURI), "file://") {
		return ""
	}
	if policy == "strict" {
		return ""
	}
	return sourceURI
}
