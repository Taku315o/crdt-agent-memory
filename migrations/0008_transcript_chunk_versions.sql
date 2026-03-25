CREATE INDEX IF NOT EXISTS idx_transcript_chunks_session_strategy
    ON transcript_chunks(session_id, chunk_strategy_version DESC, chunk_seq);
