package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const chunkStrategyVersion = 1

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

type SessionIngestRequest struct {
	SessionID      string           `json:"session_id"`
	SourceKind     string           `json:"source_kind"`
	Namespace      string           `json:"namespace"`
	RepoPath       string           `json:"repo_path,omitempty"`
	RepoRootHash   string           `json:"repo_root_hash,omitempty"`
	BranchName     string           `json:"branch_name,omitempty"`
	Title          string           `json:"title,omitempty"`
	AgentName      string           `json:"agent_name,omitempty"`
	UserIdentity   string           `json:"user_identity,omitempty"`
	StartedAtMS    int64            `json:"started_at_ms"`
	EndedAtMS      int64            `json:"ended_at_ms"`
	IngestVersion  int              `json:"ingest_version,omitempty"`
	Metadata       map[string]any   `json:"metadata_json,omitempty"`
	Sensitivity    string           `json:"sensitivity,omitempty"`
	RetentionClass string           `json:"retention_class,omitempty"`
	Messages       []SessionMessage `json:"messages"`
	ProjectKey     string           `json:"project_key,omitempty"`
}

type SessionMessage struct {
	MessageID    string         `json:"message_id,omitempty"`
	Seq          int            `json:"seq"`
	Role         string         `json:"role"`
	ToolName     string         `json:"tool_name,omitempty"`
	Content      string         `json:"content"`
	AuthoredAtMS int64          `json:"authored_at_ms"`
	Metadata     map[string]any `json:"metadata_json,omitempty"`
}

type Chunk struct {
	ChunkID      string
	ChunkSeq     int
	ChunkKind    string
	StartSeq     int
	EndSeq       int
	Text         string
	AuthoredAtMS int64
	Sensitivity  string
}

func (s *Service) IngestSession(ctx context.Context, req SessionIngestRequest) error {
	req = normalizeRequest(req)
	if err := validateRequest(req); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := upsertSession(ctx, tx, req); err != nil {
		return err
	}
	if err := upsertMessages(ctx, tx, req); err != nil {
		return err
	}
	messages, err := loadMessages(ctx, tx, req.SessionID)
	if err != nil {
		return err
	}
	chunks := buildChunks(req.SessionID, req.Sensitivity, messages)
	if err := upsertChunks(ctx, tx, req, chunks); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func normalizeRequest(req SessionIngestRequest) SessionIngestRequest {
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.SourceKind = strings.TrimSpace(req.SourceKind)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.BranchName = strings.TrimSpace(req.BranchName)
	req.ProjectKey = strings.TrimSpace(req.ProjectKey)
	if req.IngestVersion == 0 {
		req.IngestVersion = 1
	}
	if req.Sensitivity == "" {
		req.Sensitivity = "private"
	}
	if req.RetentionClass == "" {
		req.RetentionClass = "default"
	}
	for i := range req.Messages {
		req.Messages[i].Role = strings.TrimSpace(req.Messages[i].Role)
		req.Messages[i].ToolName = strings.TrimSpace(req.Messages[i].ToolName)
		req.Messages[i].Content = strings.TrimSpace(req.Messages[i].Content)
		if req.Messages[i].AuthoredAtMS == 0 {
			req.Messages[i].AuthoredAtMS = time.Now().UnixMilli()
		}
	}
	return req
}

func validateRequest(req SessionIngestRequest) error {
	if req.SessionID == "" {
		return errors.New("session_id is required")
	}
	if req.SourceKind == "" {
		return errors.New("source_kind is required")
	}
	if req.Namespace == "" {
		return errors.New("namespace is required")
	}
	if len(req.Messages) == 0 {
		return errors.New("messages is required")
	}
	return nil
}

func upsertSession(ctx context.Context, tx *sql.Tx, req SessionIngestRequest) error {
	rawMeta, err := json.Marshal(req.Metadata)
	if err != nil {
		return err
	}
	createdAtMS := time.Now().UnixMilli()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO transcript_sessions(
			session_id, source_kind, namespace, repo_path, repo_root_hash, branch_name, title,
			agent_name, user_identity, started_at_ms, ended_at_ms, ingest_version, metadata_json,
			sensitivity, retention_class, created_at_ms
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			source_kind = excluded.source_kind,
			namespace = excluded.namespace,
			repo_path = excluded.repo_path,
			repo_root_hash = excluded.repo_root_hash,
			branch_name = excluded.branch_name,
			title = excluded.title,
			agent_name = excluded.agent_name,
			user_identity = excluded.user_identity,
			started_at_ms = excluded.started_at_ms,
			ended_at_ms = excluded.ended_at_ms,
			ingest_version = excluded.ingest_version,
			metadata_json = excluded.metadata_json,
			sensitivity = excluded.sensitivity,
			retention_class = excluded.retention_class
	`, req.SessionID, req.SourceKind, req.Namespace, req.RepoPath, req.RepoRootHash, req.BranchName, req.Title,
		req.AgentName, req.UserIdentity, req.StartedAtMS, req.EndedAtMS, req.IngestVersion, string(rawMeta),
		req.Sensitivity, req.RetentionClass, createdAtMS)
	return err
}

func upsertMessages(ctx context.Context, tx *sql.Tx, req SessionIngestRequest) error {
	for _, msg := range req.Messages {
		contentHash := digest(msg.Content)
		messageID := msg.MessageID
		if messageID == "" {
			messageID = fmt.Sprintf("%s:%d", req.SessionID, msg.Seq)
		}
		rawMeta, err := json.Marshal(msg.Metadata)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO transcript_messages(
				message_id, session_id, seq, role, tool_name, content, content_hash, authored_at_ms, metadata_json
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, seq) DO UPDATE SET
				role = CASE WHEN transcript_messages.content_hash = excluded.content_hash THEN excluded.role ELSE transcript_messages.role END,
				tool_name = CASE WHEN transcript_messages.content_hash = excluded.content_hash THEN excluded.tool_name ELSE transcript_messages.tool_name END,
				content = CASE WHEN transcript_messages.content_hash = excluded.content_hash THEN excluded.content ELSE transcript_messages.content END,
				authored_at_ms = CASE WHEN transcript_messages.content_hash = excluded.content_hash THEN excluded.authored_at_ms ELSE transcript_messages.authored_at_ms END,
				metadata_json = CASE WHEN transcript_messages.content_hash = excluded.content_hash THEN excluded.metadata_json ELSE transcript_messages.metadata_json END
		`, messageID, req.SessionID, msg.Seq, msg.Role, msg.ToolName, msg.Content, contentHash, msg.AuthoredAtMS, string(rawMeta))
		if err != nil {
			return err
		}
		var existingHash string
		if err := tx.QueryRowContext(ctx, `
			SELECT content_hash FROM transcript_messages WHERE session_id = ? AND seq = ?
		`, req.SessionID, msg.Seq).Scan(&existingHash); err != nil {
			return err
		}
		if existingHash != contentHash {
			return fmt.Errorf("seq %d reused with different content", msg.Seq)
		}
	}
	return nil
}

func loadMessages(ctx context.Context, tx *sql.Tx, sessionID string) ([]SessionMessage, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT message_id, seq, role, tool_name, content, authored_at_ms, metadata_json
		FROM transcript_messages
		WHERE session_id = ?
		ORDER BY seq
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionMessage
	for rows.Next() {
		var msg SessionMessage
		var rawMeta string
		if err := rows.Scan(&msg.MessageID, &msg.Seq, &msg.Role, &msg.ToolName, &msg.Content, &msg.AuthoredAtMS, &rawMeta); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(rawMeta), &msg.Metadata)
		out = append(out, msg)
	}
	return out, rows.Err()
}

func buildChunks(sessionID, sensitivity string, messages []SessionMessage) []Chunk {
	sort.Slice(messages, func(i, j int) bool { return messages[i].Seq < messages[j].Seq })
	var chunks []Chunk
	for i := 0; i < len(messages); {
		start := i
		end := i
		if messages[i].Role == "user" {
			end = i
			for end+1 < len(messages) && messages[end+1].Role != "user" {
				end++
			}
		}
		textParts := make([]string, 0, end-start+1)
		authoredAtMS := messages[end].AuthoredAtMS
		for _, msg := range messages[start : end+1] {
			textParts = append(textParts, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
			if msg.AuthoredAtMS > authoredAtMS {
				authoredAtMS = msg.AuthoredAtMS
			}
		}
		text := strings.Join(textParts, "\n")
		chunkSeq := len(chunks) + 1
		chunks = append(chunks, Chunk{
			ChunkID:      fmt.Sprintf("%s:v%d:%d", sessionID, chunkStrategyVersion, chunkSeq),
			ChunkSeq:     chunkSeq,
			ChunkKind:    detectChunkKind(text),
			StartSeq:     messages[start].Seq,
			EndSeq:       messages[end].Seq,
			Text:         text,
			AuthoredAtMS: authoredAtMS,
			Sensitivity:  effectiveSensitivity(sensitivity, text),
		})
		i = end + 1
	}
	return chunks
}

var secretPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)`)
var artifactPattern = regexp.MustCompile(`(?i)(file://[^\s"'` + "`" + `]+|https?://[^\s"'` + "`" + `]+|/(?:Users|home|tmp|var|etc|opt)/[^\s"'` + "`" + `]+)`)

func effectiveSensitivity(defaultSensitivity, text string) string {
	if secretPattern.MatchString(text) {
		return "secret"
	}
	return defaultSensitivity
}

func detectChunkKind(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "decision"), strings.Contains(lower, "方針"), strings.Contains(lower, "採用"), strings.Contains(lower, "却下"):
		return "decision"
	case strings.Contains(lower, "reason"), strings.Contains(lower, "理由"), strings.Contains(lower, "tradeoff"), strings.Contains(lower, "トレードオフ"):
		return "rationale"
	case strings.Contains(lower, "todo"), strings.Contains(lower, "next step"), strings.Contains(lower, "action item"):
		return "task_candidate"
	case strings.Contains(lower, "error"), strings.Contains(lower, "debug"), strings.Contains(lower, "stack trace"):
		return "debug_trace"
	default:
		return "qa_pair"
	}
}

func upsertChunks(ctx context.Context, tx *sql.Tx, req SessionIngestRequest, chunks []Chunk) error {
	for _, chunk := range chunks {
		rawMeta := `{}`
		normalized := strings.ToLower(strings.TrimSpace(chunk.Text))
		_, err := tx.ExecContext(ctx, `
			INSERT INTO transcript_chunks(
				chunk_id, session_id, chunk_strategy_version, chunk_seq, chunk_kind, start_seq, end_seq,
				text, normalized_text, content_hash, authored_at_ms, source_uri, sensitivity, retention_class,
				is_indexable, metadata_json
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?)
			ON CONFLICT(chunk_id) DO UPDATE SET
				chunk_kind = excluded.chunk_kind,
				start_seq = excluded.start_seq,
				end_seq = excluded.end_seq,
				text = excluded.text,
				normalized_text = excluded.normalized_text,
				content_hash = excluded.content_hash,
				authored_at_ms = excluded.authored_at_ms,
				sensitivity = excluded.sensitivity,
				retention_class = excluded.retention_class,
				is_indexable = excluded.is_indexable,
				metadata_json = excluded.metadata_json
		`, chunk.ChunkID, req.SessionID, chunkStrategyVersion, chunk.ChunkSeq, chunk.ChunkKind, chunk.StartSeq, chunk.EndSeq,
			chunk.Text, normalized, digest(chunk.Text), chunk.AuthoredAtMS, chunk.Sensitivity, req.RetentionClass, 1, rawMeta)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO retrieval_units(
				unit_id, source_type, source_id, memory_space, namespace, unit_kind, title, body, body_hash,
				authored_at_ms, sensitivity, retention_class, state, source_uri, project_key, branch_name, schema_version
			) VALUES(?, 'transcript_chunk', ?, 'transcript', ?, ?, '', ?, ?, ?, ?, ?, 'active', '', ?, ?, 1)
			ON CONFLICT(unit_id) DO UPDATE SET
				namespace = excluded.namespace,
				unit_kind = excluded.unit_kind,
				body = excluded.body,
				body_hash = excluded.body_hash,
				authored_at_ms = excluded.authored_at_ms,
				sensitivity = excluded.sensitivity,
				retention_class = excluded.retention_class,
				project_key = excluded.project_key,
				branch_name = excluded.branch_name
		`, chunk.ChunkID, chunk.ChunkID, req.Namespace, chunk.ChunkKind, chunk.Text, digest(chunk.Text), chunk.AuthoredAtMS,
			chunk.Sensitivity, req.RetentionClass, req.ProjectKey, req.BranchName)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO retrieval_index_queue(queue_id, unit_id, enqueued_at_ms)
			VALUES(?, ?, ?)
		`, uuid.NewString(), chunk.ChunkID, time.Now().UnixMilli()); err != nil {
			return err
		}
		if err := upsertTranscriptArtifacts(ctx, tx, req.Namespace, chunk); err != nil {
			return err
		}
		if err := upsertPromotionCandidate(ctx, tx, req, chunk); err != nil {
			return err
		}
	}
	return nil
}

func upsertPromotionCandidate(ctx context.Context, tx *sql.Tx, req SessionIngestRequest, chunk Chunk) error {
	if !isPromotionCandidateKind(chunk.ChunkKind) {
		return nil
	}
	now := time.Now().UnixMilli()
	candidateID := "cand_" + digest(chunk.ChunkID)
	subject := inferCandidateSubject(chunk.ChunkKind, chunk.Text)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_candidates(
			candidate_id, namespace, candidate_type, status, subject, body, source_uri,
			authored_at_ms, created_at_ms, updated_at_ms, author_agent_id, origin_peer_id,
			sensitivity, retention_class, project_key, branch_name, metadata_json
		) VALUES(?, ?, ?, 'pending', ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}')
		ON CONFLICT(candidate_id) DO UPDATE SET
			namespace = excluded.namespace,
			candidate_type = excluded.candidate_type,
			subject = excluded.subject,
			body = excluded.body,
			authored_at_ms = excluded.authored_at_ms,
			updated_at_ms = excluded.updated_at_ms,
			author_agent_id = excluded.author_agent_id,
			sensitivity = excluded.sensitivity,
			retention_class = excluded.retention_class,
			project_key = excluded.project_key,
			branch_name = excluded.branch_name
		WHERE memory_candidates.status = 'pending'
	`, candidateID, req.Namespace, chunk.ChunkKind, subject, chunk.Text, chunk.AuthoredAtMS, now, now,
		"agent/ingest", "peer/local", chunk.Sensitivity, req.RetentionClass, req.ProjectKey, req.BranchName); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_candidate_chunks WHERE candidate_id = ?`, candidateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_candidate_chunks(link_id, candidate_id, chunk_id, ordinal)
		VALUES(?, ?, ?, 0)
	`, uuid.NewString(), candidateID, chunk.ChunkID); err != nil {
		return err
	}
	return nil
}

func isPromotionCandidateKind(kind string) bool {
	switch kind {
	case "decision", "task_candidate", "rationale", "debug_trace":
		return true
	default:
		return false
	}
}

func inferCandidateSubject(kind, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return kind
	}
	firstLine := text
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	if idx := strings.Index(firstLine, ":"); idx >= 0 && idx+1 < len(firstLine) {
		firstLine = strings.TrimSpace(firstLine[idx+1:])
	}
	firstLine = strings.TrimSpace(firstLine)
	if firstLine == "" {
		return kind
	}
	runes := []rune(firstLine)
	if len(runes) > 80 {
		firstLine = string(runes[:80])
	}
	return firstLine
}

func upsertTranscriptArtifacts(ctx context.Context, tx *sql.Tx, namespace string, chunk Chunk) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM transcript_artifact_spans WHERE chunk_id = ?`, chunk.ChunkID); err != nil {
		return err
	}
	matches := artifactPattern.FindAllStringIndex(chunk.Text, -1)
	for _, match := range matches {
		start, end := match[0], match[1]
		uri := chunk.Text[start:end]
		artifactID := "artifact_" + digest(uri)
		title := path.Base(strings.TrimPrefix(uri, "file://"))
		startLine, endLine := lineRange(chunk.Text, start, end)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO private_artifact_refs(artifact_id, local_namespace, uri, content_hash, title, mime_type, authored_at_ms)
			VALUES(?, ?, ?, ?, ?, '', ?)
			ON CONFLICT(artifact_id) DO UPDATE SET
				local_namespace = excluded.local_namespace,
				uri = excluded.uri,
				content_hash = excluded.content_hash,
				title = excluded.title,
				authored_at_ms = excluded.authored_at_ms
		`, artifactID, namespace, uri, digest(uri), title, chunk.AuthoredAtMS); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcript_artifact_spans(
				span_id, chunk_id, artifact_id, start_offset, end_offset, start_line, end_line, quote_hash, authored_at_ms
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, uuid.NewString(), chunk.ChunkID, artifactID, start, end, startLine, endLine, digest(uri), chunk.AuthoredAtMS); err != nil {
			return err
		}
	}
	return nil
}

func lineRange(text string, start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	startLine := 1 + strings.Count(text[:start], "\n")
	endLine := 1 + strings.Count(text[:end], "\n")
	return startLine, endLine
}

func digest(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}
