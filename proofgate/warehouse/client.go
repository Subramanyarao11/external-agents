package warehouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
)

type Config struct {
	Host        string
	Token       string
	WarehouseID string
	Catalog     string
	Schema      string
	HTTPClient  *http.Client
}

type Client struct {
	config Config
}

type IngestResult struct {
	StatementID string `json:"statement_id"`
	State       string `json:"state"`
	EventID     string `json:"event_id"`
	PayloadHash string `json:"payload_hash"`
}

type HistoryQuery struct {
	RepositoryID        string
	ExcludeEventID      string
	Before              time.Time
	SnapshotAt          time.Time
	BaselineWindowDays  int
	ChangedFileCount    int
	ImpactedEntityCount int
	DependencyDepth     int
	SensitiveComponents []string
}

type ReviewDecision struct {
	Available       bool      `json:"available"`
	DecisionEventID string    `json:"decision_event_id,omitempty"`
	Action          string    `json:"action,omitempty"`
	ActorID         string    `json:"actor_id,omitempty"`
	OccurredAt      time.Time `json:"occurred_at,omitempty"`
}

type statementParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type statementRequest struct {
	WarehouseID string               `json:"warehouse_id"`
	Catalog     string               `json:"catalog"`
	Schema      string               `json:"schema"`
	Statement   string               `json:"statement"`
	WaitTimeout string               `json:"wait_timeout"`
	Disposition string               `json:"disposition"`
	Format      string               `json:"format"`
	Parameters  []statementParameter `json:"parameters"`
	RowLimit    int64                `json:"row_limit,omitempty"`
	ByteLimit   int64                `json:"byte_limit,omitempty"`
}

type statementResponse struct {
	StatementID string `json:"statement_id"`
	Status      struct {
		State string `json:"state"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"status"`
	Result struct {
		DataArray [][]*string `json:"data_array"`
	} `json:"result"`
}

func NewClient(config Config) (*Client, error) {
	config.Host = strings.TrimRight(strings.TrimSpace(config.Host), "/")
	for name, value := range map[string]string{
		"host": config.Host, "token": config.Token, "warehouse id": config.WarehouseID,
		"catalog": config.Catalog, "schema": config.Schema,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("Databricks %s is required", name)
		}
	}
	parsed, err := url.Parse(config.Host)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("Databricks host must be an https URL")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 55 * time.Second}
	}
	return &Client{config: config}, nil
}

func (client *Client) Ingest(ctx context.Context, event ChangeEvent) (IngestResult, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return IngestResult{}, err
	}
	request := statementRequest{
		WarehouseID: client.config.WarehouseID,
		Catalog:     client.config.Catalog,
		Schema:      client.config.Schema,
		Statement: `MERGE INTO bronze_checkpoint_events AS target
USING (SELECT
  :event_id AS event_id,
  :event_type AS event_type,
  :schema_version AS schema_version,
  CAST(:occurred_at AS TIMESTAMP) AS occurred_at,
  :repo_id AS repo_id,
  :commit_sha AS commit_sha,
  :checkpoint_id AS checkpoint_id,
  :source_adapter AS source_adapter,
  :policy_version AS policy_version,
  :payload_hash AS payload_hash,
  :redaction_version AS redaction_version,
  CAST(:dropped_field_count AS INT) AS dropped_field_count,
  :payload_json AS payload_json
) AS source
ON target.event_id = source.event_id
WHEN NOT MATCHED THEN INSERT (
  event_id, event_type, schema_version, occurred_at, ingested_at,
  repo_id, commit_sha, checkpoint_id, source_adapter, policy_version,
  payload_hash, redaction_version, dropped_field_count, payload_json
) VALUES (
  source.event_id, source.event_type, source.schema_version, source.occurred_at, current_timestamp(),
  source.repo_id, source.commit_sha, source.checkpoint_id, source.source_adapter, source.policy_version,
  source.payload_hash, source.redaction_version, source.dropped_field_count, source.payload_json
)`,
		WaitTimeout: "50s",
		Disposition: "INLINE",
		Format:      "JSON_ARRAY",
		Parameters: []statementParameter{
			{Name: "event_id", Value: event.EventID, Type: "STRING"},
			{Name: "event_type", Value: event.EventType, Type: "STRING"},
			{Name: "schema_version", Value: event.SchemaVersion, Type: "STRING"},
			{Name: "occurred_at", Value: event.OccurredAt.Format(time.RFC3339Nano), Type: "STRING"},
			{Name: "repo_id", Value: event.RepoID, Type: "STRING"},
			{Name: "commit_sha", Value: event.CommitSHA, Type: "STRING"},
			{Name: "checkpoint_id", Value: event.CheckpointID, Type: "STRING"},
			{Name: "source_adapter", Value: event.SourceAdapter, Type: "STRING"},
			{Name: "policy_version", Value: event.PolicyVersion, Type: "STRING"},
			{Name: "payload_hash", Value: event.PayloadHash, Type: "STRING"},
			{Name: "redaction_version", Value: event.RedactionVersion, Type: "STRING"},
			{Name: "dropped_field_count", Value: fmt.Sprintf("%d", event.DroppedFieldCount), Type: "INT"},
			{Name: "payload_json", Value: string(payload), Type: "STRING"},
		},
	}
	response, err := client.runStatement(ctx, request)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{StatementID: response.StatementID, State: response.Status.State, EventID: event.EventID, PayloadHash: event.PayloadHash}, nil
}

func (client *Client) LookupHistory(ctx context.Context, query HistoryQuery) (contracts.HistoricalEvidence, error) {
	query.RepositoryID = strings.TrimSpace(query.RepositoryID)
	if query.RepositoryID == "" {
		return contracts.HistoricalEvidence{}, errors.New("history repository id is required")
	}
	if query.Before.IsZero() || query.SnapshotAt.IsZero() {
		return contracts.HistoricalEvidence{}, errors.New("history timestamps are required")
	}
	if query.BaselineWindowDays <= 0 {
		query.BaselineWindowDays = 30
	}
	sensitiveJSON, err := json.Marshal(query.SensitiveComponents)
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	baselineStart := query.Before.AddDate(0, 0, -query.BaselineWindowDays)
	request := statementRequest{
		WarehouseID: client.config.WarehouseID,
		Catalog:     client.config.Catalog,
		Schema:      client.config.Schema,
		Statement: `WITH history AS (
  SELECT *
  FROM gold_change_risk_features
  WHERE repo_id = :repo_id
    AND event_id <> :exclude_event_id
    AND occurred_at >= CAST(:baseline_start AS TIMESTAMP)
    AND occurred_at < CAST(:before AS TIMESTAMP)
), similar AS (
  SELECT *
  FROM history
  WHERE ABS(changed_file_count - CAST(:changed_file_count AS INT))
          <= GREATEST(2, CEIL(CAST(:changed_file_count AS DOUBLE) * 0.5))
    AND ABS(impacted_entity_count - CAST(:impacted_entity_count AS INT))
          <= GREATEST(2, CEIL(CAST(:impacted_entity_count AS DOUBLE) * 0.5))
    AND ABS(dependency_depth - CAST(:dependency_depth AS INT)) <= 1
), component_history AS (
  SELECT *
  FROM history
  WHERE CAST(:sensitive_component_count AS INT) > 0
    AND arrays_overlap(
      COALESCE(sensitive_components, CAST(array() AS ARRAY<STRING>)),
      from_json(:sensitive_components_json, 'ARRAY<STRING>')
    )
), baseline_agg AS (
  SELECT
    COUNT(*) AS baseline_change_count,
    COALESCE(percentile_approx(changed_file_count, 0.95), 0) AS normal_file_count_p95,
    COALESCE(percentile_approx(impacted_entity_count, 0.95), 0) AS normal_impact_count_p95
  FROM history
), similar_agg AS (
  SELECT
    COUNT(*) AS similar_change_count,
    COALESCE(AVG(CASE WHEN test_failed > 0 OR required_test_missing OR decision = 'APPROVAL_REQUIRED' THEN 1.0 ELSE 0.0 END), 0.0) AS similar_failure_rate,
    GREATEST(COALESCE(SUM(CASE WHEN test_failed > 0 OR required_test_missing THEN 1 ELSE 0 END), 0) - 1, 0) AS repeated_failure_count,
    to_json(slice(sort_array(collect_set(event_id)), 1, 10)) AS similar_evidence_ids
  FROM similar
), component_agg AS (
  SELECT
    COALESCE(AVG(CASE WHEN test_failed > 0 OR required_test_missing OR decision = 'APPROVAL_REQUIRED' THEN 1.0 ELSE 0.0 END), 0.0) AS component_failure_rate
  FROM component_history
)
SELECT
  baseline_change_count,
  normal_file_count_p95,
  normal_impact_count_p95,
  similar_change_count,
  similar_failure_rate,
  component_failure_rate,
  repeated_failure_count,
  similar_evidence_ids
FROM baseline_agg
CROSS JOIN similar_agg
CROSS JOIN component_agg`,
		WaitTimeout: "50s",
		Disposition: "INLINE",
		Format:      "JSON_ARRAY",
		RowLimit:    1,
		ByteLimit:   16 << 10,
		Parameters: []statementParameter{
			{Name: "repo_id", Value: query.RepositoryID, Type: "STRING"},
			{Name: "exclude_event_id", Value: strings.TrimSpace(query.ExcludeEventID), Type: "STRING"},
			{Name: "baseline_start", Value: baselineStart.UTC().Format(time.RFC3339Nano), Type: "STRING"},
			{Name: "before", Value: query.Before.UTC().Format(time.RFC3339Nano), Type: "STRING"},
			{Name: "changed_file_count", Value: fmt.Sprintf("%d", query.ChangedFileCount), Type: "INT"},
			{Name: "impacted_entity_count", Value: fmt.Sprintf("%d", query.ImpactedEntityCount), Type: "INT"},
			{Name: "dependency_depth", Value: fmt.Sprintf("%d", query.DependencyDepth), Type: "INT"},
			{Name: "sensitive_component_count", Value: fmt.Sprintf("%d", len(query.SensitiveComponents)), Type: "INT"},
			{Name: "sensitive_components_json", Value: string(sensitiveJSON), Type: "STRING"},
		},
	}
	response, err := client.runStatement(ctx, request)
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	if len(response.Result.DataArray) != 1 {
		return contracts.HistoricalEvidence{}, fmt.Errorf("history query returned %d rows, want 1", len(response.Result.DataArray))
	}
	row := response.Result.DataArray[0]
	if len(row) != 8 {
		return contracts.HistoricalEvidence{}, fmt.Errorf("history query returned %d columns, want 8", len(row))
	}
	baselineCount, err := parseIntCell(row[0], "baseline_change_count")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	fileP95, err := parseFloatCell(row[1], "normal_file_count_p95")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	impactP95, err := parseFloatCell(row[2], "normal_impact_count_p95")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	similarCount, err := parseIntCell(row[3], "similar_change_count")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	similarFailureRate, err := parseFloatCell(row[4], "similar_failure_rate")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	componentFailureRate, err := parseFloatCell(row[5], "component_failure_rate")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	repeatedFailures, err := parseIntCell(row[6], "repeated_failure_count")
	if err != nil {
		return contracts.HistoricalEvidence{}, err
	}
	evidenceIDs := []string{}
	if row[7] != nil && strings.TrimSpace(*row[7]) != "" {
		if err := json.Unmarshal([]byte(*row[7]), &evidenceIDs); err != nil {
			return contracts.HistoricalEvidence{}, fmt.Errorf("parse similar_evidence_ids: %w", err)
		}
	}
	if len(evidenceIDs) > 20 {
		return contracts.HistoricalEvidence{}, errors.New("history query returned too many evidence ids")
	}
	return contracts.HistoricalEvidence{
		Available:            true,
		SnapshotAt:           query.SnapshotAt.UTC(),
		BaselineWindowDays:   query.BaselineWindowDays,
		BaselineChangeCount:  baselineCount,
		NormalFileCountP95:   fileP95,
		NormalImpactCountP95: impactP95,
		SimilarChangeCount:   similarCount,
		SimilarFailureRate:   similarFailureRate,
		ComponentFailureRate: componentFailureRate,
		RepeatedFailureCount: repeatedFailures,
		SimilarEvidenceIDs:   evidenceIDs,
	}, nil
}

func (client *Client) LookupReview(ctx context.Context, eventID string) (ReviewDecision, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ReviewDecision{}, errors.New("review event id is required")
	}
	request := statementRequest{
		WarehouseID: client.config.WarehouseID,
		Catalog:     client.config.Catalog,
		Schema:      client.config.Schema,
		Statement: `SELECT
  decision_event_id,
  action,
  actor_id,
  CAST(occurred_at AS STRING) AS occurred_at
FROM policy_decision_events
WHERE gate_event_id = :gate_event_id
ORDER BY occurred_at DESC
LIMIT 1`,
		WaitTimeout: "50s",
		Disposition: "INLINE",
		Format:      "JSON_ARRAY",
		RowLimit:    1,
		ByteLimit:   4 << 10,
		Parameters: []statementParameter{
			{Name: "gate_event_id", Value: eventID, Type: "STRING"},
		},
	}
	response, err := client.runStatement(ctx, request)
	if err != nil {
		return ReviewDecision{}, err
	}
	if len(response.Result.DataArray) == 0 {
		return ReviewDecision{Available: false}, nil
	}
	if len(response.Result.DataArray) != 1 || len(response.Result.DataArray[0]) != 4 {
		return ReviewDecision{}, errors.New("review query returned an invalid shape")
	}
	row := response.Result.DataArray[0]
	for index, cell := range row {
		if cell == nil || strings.TrimSpace(*cell) == "" {
			return ReviewDecision{}, fmt.Errorf("review column %d is empty", index)
		}
	}
	action := strings.ToUpper(strings.TrimSpace(*row[1]))
	if action != "APPROVE" && action != "REJECT" {
		return ReviewDecision{}, fmt.Errorf("review action %q is invalid", action)
	}
	occurredAt, err := parseDatabricksTimestamp(*row[3])
	if err != nil {
		return ReviewDecision{}, fmt.Errorf("parse review occurred_at: %w", err)
	}
	return ReviewDecision{
		Available:       true,
		DecisionEventID: strings.TrimSpace(*row[0]),
		Action:          action,
		ActorID:         strings.TrimSpace(*row[2]),
		OccurredAt:      occurredAt,
	}, nil
}

func (client *Client) runStatement(ctx context.Context, request statementRequest) (statementResponse, error) {
	response, err := client.execute(ctx, http.MethodPost, "/api/2.0/sql/statements", request)
	if err != nil {
		return statementResponse{}, err
	}
	for response.Status.State == "PENDING" || response.Status.State == "RUNNING" {
		select {
		case <-ctx.Done():
			return statementResponse{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		response, err = client.execute(ctx, http.MethodGet, "/api/2.0/sql/statements/"+url.PathEscape(response.StatementID), nil)
		if err != nil {
			return statementResponse{}, err
		}
	}
	if response.Status.State != "SUCCEEDED" {
		message := "Databricks statement did not succeed"
		if response.Status.Error != nil && response.Status.Error.Message != "" {
			message = response.Status.Error.Message
		}
		return statementResponse{}, fmt.Errorf("%s (state=%s, statement_id=%s)", message, response.Status.State, response.StatementID)
	}
	return response, nil
}

func parseIntCell(cell *string, name string) (int, error) {
	if cell == nil {
		return 0, fmt.Errorf("history %s is null", name)
	}
	value, err := strconv.Atoi(*cell)
	if err != nil {
		return 0, fmt.Errorf("parse history %s: %w", name, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("history %s cannot be negative", name)
	}
	return value, nil
}

func parseFloatCell(cell *string, name string) (float64, error) {
	if cell == nil {
		return 0, fmt.Errorf("history %s is null", name)
	}
	value, err := strconv.ParseFloat(*cell, 64)
	if err != nil {
		return 0, fmt.Errorf("parse history %s: %w", name, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("history %s cannot be negative", name)
	}
	return value, nil
}

func parseDatabricksTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	formats := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}

func (client *Client) execute(ctx context.Context, method, path string, body any) (statementResponse, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return statementResponse{}, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.config.Host+path, reader)
	if err != nil {
		return statementResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.config.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return statementResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return statementResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statementResponse{}, fmt.Errorf("Databricks API returned %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var result statementResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return statementResponse{}, fmt.Errorf("decode Databricks response: %w", err)
	}
	return result, nil
}
