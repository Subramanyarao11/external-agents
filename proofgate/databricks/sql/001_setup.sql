USE CATALOG IDENTIFIER(:catalog_name);
USE SCHEMA IDENTIFIER(:schema_name);

CREATE TABLE IF NOT EXISTS bronze_checkpoint_events (
  event_id STRING NOT NULL COMMENT 'Idempotency key from the runner',
  event_type STRING NOT NULL,
  schema_version STRING NOT NULL,
  occurred_at TIMESTAMP NOT NULL,
  ingested_at TIMESTAMP NOT NULL,
  repo_id STRING NOT NULL COMMENT 'Synthetic or explicitly permitted repository identifier',
  commit_sha STRING,
  checkpoint_id STRING,
  source_adapter STRING NOT NULL,
  policy_version STRING NOT NULL,
  payload_hash STRING NOT NULL,
  redaction_version STRING NOT NULL,
  dropped_field_count INT NOT NULL,
  payload_json STRING NOT NULL COMMENT 'Allowlisted ChangeEvent only; no raw prompt or code'
)
USING DELTA
TBLPROPERTIES (
  'delta.enableChangeDataFeed' = 'true',
  'proofgate.data_classification' = 'allowlisted_metadata'
);

CREATE TABLE IF NOT EXISTS silver_checkpoint_events (
  event_id STRING NOT NULL,
  event_type STRING NOT NULL,
  schema_version STRING NOT NULL,
  occurred_at TIMESTAMP NOT NULL,
  ingested_at TIMESTAMP NOT NULL,
  repo_id STRING NOT NULL,
  commit_sha STRING,
  checkpoint_id STRING NOT NULL,
  source_adapter STRING NOT NULL,
  policy_version STRING NOT NULL,
  payload_hash STRING NOT NULL,
  redaction_version STRING NOT NULL,
  dropped_field_count INT NOT NULL,
  payload_json STRING NOT NULL,
  validated_at TIMESTAMP NOT NULL,
  source_event_id STRING NOT NULL
)
USING DELTA
TBLPROPERTIES ('delta.enableChangeDataFeed' = 'true');

CREATE TABLE IF NOT EXISTS silver_quarantine (
  event_id STRING,
  payload_hash STRING,
  quarantine_reason STRING NOT NULL,
  source_ingested_at TIMESTAMP,
  quarantined_at TIMESTAMP NOT NULL
)
USING DELTA;

CREATE TABLE IF NOT EXISTS gold_change_risk_features (
  event_id STRING NOT NULL,
  repo_id STRING NOT NULL,
  checkpoint_id STRING NOT NULL,
  commit_sha STRING,
  policy_version STRING NOT NULL,
  occurred_at TIMESTAMP NOT NULL,
  decision STRING NOT NULL,
  risk_score INT NOT NULL,
  agent_family STRING,
  model_family STRING,
  changed_file_count INT,
  changed_line_count INT,
  impacted_entity_count INT,
  dependency_depth INT,
  max_dependent_count INT,
  impact_analysis_source STRING,
  impact_analysis_complete BOOLEAN,
  sensitive_components ARRAY<STRING> COMMENT 'Allowlisted component categories only; never source paths',
  test_total INT,
  test_failed INT,
  required_test_missing BOOLEAN,
  provenance_complete BOOLEAN,
  similar_change_count INT,
  similar_failure_rate DOUBLE,
  component_failure_rate DOUBLE,
  reason_codes ARRAY<STRING>,
  hard_stop_codes ARRAY<STRING>,
  source_event_id STRING NOT NULL,
  history_snapshot_at TIMESTAMP,
  feature_generated_at TIMESTAMP NOT NULL
)
USING DELTA
TBLPROPERTIES ('delta.enableChangeDataFeed' = 'true');

CREATE TABLE IF NOT EXISTS similar_change_documents (
  event_id STRING NOT NULL,
  repo_id STRING NOT NULL,
  checkpoint_id STRING NOT NULL,
  occurred_at TIMESTAMP NOT NULL,
  decision STRING NOT NULL,
  risk_score INT NOT NULL,
  similarity_summary STRING NOT NULL,
  source_event_id STRING NOT NULL
)
USING DELTA
TBLPROPERTIES ('delta.enableChangeDataFeed' = 'true');

CREATE TABLE IF NOT EXISTS policy_decision_events (
  decision_event_id STRING NOT NULL,
  gate_event_id STRING NOT NULL,
  checkpoint_id STRING NOT NULL,
  action STRING NOT NULL,
  actor_id STRING NOT NULL,
  expected_version INT NOT NULL,
  decision_reason STRING,
  policy_version STRING NOT NULL,
  occurred_at TIMESTAMP NOT NULL,
  idempotency_key STRING NOT NULL
)
USING DELTA
TBLPROPERTIES ('delta.enableChangeDataFeed' = 'true');

CREATE OR REPLACE VIEW fleet_gate_metrics AS
SELECT
  date_trunc('DAY', occurred_at) AS metric_day,
  repo_id,
  policy_version,
  count(*) AS change_count,
  count_if(provenance_complete) AS provenance_complete_count,
  count_if(decision = 'PASS') AS pass_count,
  count_if(decision = 'WARN') AS warn_count,
  count_if(decision = 'APPROVAL_REQUIRED') AS approval_required_count,
  avg(risk_score) AS average_risk_score,
  max(max_dependent_count) AS maximum_dependent_count,
  count_if(impact_analysis_complete) AS graph_complete_count,
  avg(CASE WHEN test_failed > 0 THEN 1.0 ELSE 0.0 END) AS test_failure_rate
FROM gold_change_risk_features
GROUP BY ALL;
