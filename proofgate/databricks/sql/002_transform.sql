USE CATALOG IDENTIFIER(:catalog_name);
USE SCHEMA IDENTIFIER(:schema_name);

MERGE INTO silver_quarantine AS target
USING (
  SELECT
    event_id,
    payload_hash,
    CASE
      WHEN schema_version <> '1.0' THEN 'UNSUPPORTED_SCHEMA_VERSION'
      WHEN checkpoint_id IS NULL OR trim(checkpoint_id) = '' THEN 'MISSING_CHECKPOINT_ID'
      WHEN redaction_version <> 'proofgate-allowlist-v1' THEN 'UNSUPPORTED_REDACTION_VERSION'
      WHEN get_json_object(payload_json, '$.event_id') IS NULL THEN 'INVALID_PAYLOAD_JSON'
      ELSE 'UNKNOWN_VALIDATION_ERROR'
    END AS quarantine_reason,
    ingested_at AS source_ingested_at,
    current_timestamp() AS quarantined_at
  FROM bronze_checkpoint_events
  WHERE schema_version <> '1.0'
     OR checkpoint_id IS NULL
     OR trim(checkpoint_id) = ''
     OR redaction_version <> 'proofgate-allowlist-v1'
     OR get_json_object(payload_json, '$.event_id') IS NULL
) AS source
ON target.event_id <=> source.event_id
AND target.payload_hash <=> source.payload_hash
AND target.quarantine_reason = source.quarantine_reason
WHEN NOT MATCHED THEN INSERT *;

MERGE INTO silver_checkpoint_events AS target
USING (
  SELECT
    event_id,
    event_type,
    schema_version,
    occurred_at,
    ingested_at,
    repo_id,
    commit_sha,
    checkpoint_id,
    source_adapter,
    policy_version,
    payload_hash,
    redaction_version,
    dropped_field_count,
    payload_json,
    current_timestamp() AS validated_at,
    event_id AS source_event_id
  FROM bronze_checkpoint_events
  WHERE schema_version = '1.0'
    AND checkpoint_id IS NOT NULL
    AND trim(checkpoint_id) <> ''
    AND redaction_version = 'proofgate-allowlist-v1'
    AND get_json_object(payload_json, '$.event_id') IS NOT NULL
) AS source
ON target.event_id = source.event_id
WHEN MATCHED AND target.payload_hash <> source.payload_hash THEN UPDATE SET
  target.payload_hash = source.payload_hash,
  target.payload_json = source.payload_json,
  target.validated_at = source.validated_at,
  target.dropped_field_count = source.dropped_field_count
WHEN NOT MATCHED THEN INSERT *;

MERGE INTO gold_change_risk_features AS target
USING (
  SELECT
    event_id,
    repo_id,
    checkpoint_id,
    commit_sha,
    policy_version,
    occurred_at,
    get_json_object(payload_json, '$.decision') AS decision,
    CAST(get_json_object(payload_json, '$.score') AS INT) AS risk_score,
    get_json_object(payload_json, '$.agent_family') AS agent_family,
    get_json_object(payload_json, '$.model_family') AS model_family,
    CAST(get_json_object(payload_json, '$.changed_file_count') AS INT) AS changed_file_count,
    CAST(get_json_object(payload_json, '$.changed_line_count') AS INT) AS changed_line_count,
    CAST(get_json_object(payload_json, '$.impacted_entity_count') AS INT) AS impacted_entity_count,
    CAST(get_json_object(payload_json, '$.dependency_depth') AS INT) AS dependency_depth,
    CAST(get_json_object(payload_json, '$.max_dependent_count') AS INT) AS max_dependent_count,
    get_json_object(payload_json, '$.impact_analysis_source') AS impact_analysis_source,
    CAST(get_json_object(payload_json, '$.impact_analysis_complete') AS BOOLEAN) AS impact_analysis_complete,
    from_json(get_json_object(payload_json, '$.sensitive_components'), 'ARRAY<STRING>') AS sensitive_components,
    CAST(get_json_object(payload_json, '$.test_total') AS INT) AS test_total,
    CAST(get_json_object(payload_json, '$.test_failed') AS INT) AS test_failed,
    CAST(get_json_object(payload_json, '$.required_test_missing') AS BOOLEAN) AS required_test_missing,
    CAST(get_json_object(payload_json, '$.provenance_complete') AS BOOLEAN) AS provenance_complete,
    CAST(get_json_object(payload_json, '$.similar_change_count') AS INT) AS similar_change_count,
    CAST(get_json_object(payload_json, '$.similar_failure_rate') AS DOUBLE) AS similar_failure_rate,
    CAST(get_json_object(payload_json, '$.component_failure_rate') AS DOUBLE) AS component_failure_rate,
    from_json(get_json_object(payload_json, '$.reason_codes'), 'ARRAY<STRING>') AS reason_codes,
    from_json(get_json_object(payload_json, '$.hard_stop_codes'), 'ARRAY<STRING>') AS hard_stop_codes,
    event_id AS source_event_id,
    CAST(get_json_object(payload_json, '$.history_snapshot_at') AS TIMESTAMP) AS history_snapshot_at,
    current_timestamp() AS feature_generated_at
  FROM silver_checkpoint_events
) AS source
ON target.event_id = source.event_id
WHEN MATCHED THEN UPDATE SET *
WHEN NOT MATCHED THEN INSERT *;

MERGE INTO similar_change_documents AS target
USING (
  SELECT
    event_id,
    repo_id,
    checkpoint_id,
    occurred_at,
    get_json_object(payload_json, '$.decision') AS decision,
    CAST(get_json_object(payload_json, '$.score') AS INT) AS risk_score,
    get_json_object(payload_json, '$.safe_similarity_summary') AS similarity_summary,
    event_id AS source_event_id
  FROM silver_checkpoint_events
  WHERE get_json_object(payload_json, '$.safe_similarity_summary') IS NOT NULL
) AS source
ON target.event_id = source.event_id
WHEN MATCHED THEN UPDATE SET *
WHEN NOT MATCHED THEN INSERT *;
