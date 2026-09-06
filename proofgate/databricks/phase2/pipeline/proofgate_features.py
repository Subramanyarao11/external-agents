"""Incremental Lakeflow tables for near-real-time ProofGate fleet signals.

This pipeline intentionally processes the already allowlisted silver contract.
Raw prompts and source code never enter the flow.
"""

from pyspark import pipelines as dp
from pyspark.sql import functions as F


catalog = spark.conf.get("proofgate.catalog")
schema = spark.conf.get("proofgate.schema")


@dp.table(
    name="live_checkpoint_events",
    comment="Incremental, allowlisted ProofGate checkpoint events",
    table_properties={"quality": "silver", "proofgate.raw_content": "excluded"},
)
def live_checkpoint_events():
    return (
        spark.readStream.table(f"{catalog}.{schema}.silver_checkpoint_events")
        .select(
            "event_id",
            "occurred_at",
            "repo_id",
            "checkpoint_id",
            "source_adapter",
            "policy_version",
            "payload_hash",
            "payload_json",
        )
        .withColumn("processed_at", F.current_timestamp())
    )


@dp.materialized_view(
    name="live_gate_metrics",
    comment="Operational risk and evidence coverage derived from the live stream",
    table_properties={"quality": "gold"},
)
def live_gate_metrics():
    return (
        spark.read.table(f"{catalog}.{schema}.gold_change_risk_features")
        .groupBy(
            F.window("occurred_at", "1 day").alias("metric_window"),
            "repo_id",
            "policy_version",
        )
        .agg(
            F.count("*").alias("change_count"),
            F.sum(F.when(F.col("decision") == "PASS", 1).otherwise(0)).alias("pass_count"),
            F.sum(
                F.when(F.col("decision") == "APPROVAL_REQUIRED", 1).otherwise(0)
            ).alias("approval_required_count"),
            F.avg("risk_score").alias("average_risk_score"),
            F.max("max_dependent_count").alias("maximum_dependent_count"),
            F.avg(F.when(F.col("impact_analysis_complete"), 1.0).otherwise(0.0)).alias(
                "graph_complete_rate"
            ),
            F.avg(F.when(F.col("provenance_complete"), 1.0).otherwise(0.0)).alias(
                "provenance_rate"
            ),
        )
    )
