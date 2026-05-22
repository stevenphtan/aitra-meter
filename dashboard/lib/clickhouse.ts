/**
 * Thin HTTP client for ClickHouse.
 * Uses the ClickHouse HTTP interface (port 8123) with FORMAT JSONEachRow.
 * No native driver dependency — works in any Node.js environment.
 */

export interface ChargebackRow {
  namespace: string;
  workload: string;
  model: string;
  hardware: string;
  calibration_tier: string;
  attribution_method: string;
  energy_joules: number;
  token_count: number;
}

const CLICKHOUSE_URL = process.env.CLICKHOUSE_URL ?? "http://localhost:8123";
const CLICKHOUSE_USER = process.env.CLICKHOUSE_USER ?? "default";
const CLICKHOUSE_PASSWORD = process.env.CLICKHOUSE_PASSWORD ?? "";
const CLICKHOUSE_DATABASE = process.env.CLICKHOUSE_DATABASE ?? "default";

/** Execute a ClickHouse query and return rows as parsed objects. */
export async function queryClickHouse<T>(sql: string): Promise<T[]> {
  const url = new URL("/", CLICKHOUSE_URL);
  url.searchParams.set("database", CLICKHOUSE_DATABASE);
  url.searchParams.set("default_format", "JSONEachRow");

  const headers: Record<string, string> = {
    "Content-Type": "text/plain",
  };
  if (CLICKHOUSE_USER) {
    headers["X-ClickHouse-User"] = CLICKHOUSE_USER;
  }
  if (CLICKHOUSE_PASSWORD) {
    headers["X-ClickHouse-Key"] = CLICKHOUSE_PASSWORD;
  }

  const res = await fetch(url.toString(), {
    method: "POST",
    headers,
    body: sql + " FORMAT JSONEachRow",
    next: { revalidate: 0 },
  });

  if (!res.ok) {
    const body = await res.text();
    throw new Error(`ClickHouse query failed (${res.status}): ${body}`);
  }

  const text = await res.text();
  if (!text.trim()) return [];

  return text
    .trim()
    .split("\n")
    .map((line) => JSON.parse(line) as T);
}

/**
 * Fetch 30-day chargeback aggregates grouped by namespace × workload × model × hardware.
 * Returns raw joules so the client can apply PUE and cost/kWh without a re-fetch.
 */
export async function queryChargeback(days: number): Promise<ChargebackRow[]> {
  const sql = `
    SELECT
      namespace,
      workload,
      model,
      hardware,
      any(calibration_tier)    AS calibration_tier,
      any(attribution_method)  AS attribution_method,
      SUM(energy_joules)       AS energy_joules,
      SUM(output_tokens)       AS token_count
    FROM aitra_measurements
    WHERE timestamp >= now() - INTERVAL ${days} DAY
    GROUP BY namespace, workload, model, hardware
    ORDER BY energy_joules DESC
  `;
  return queryClickHouse<ChargebackRow>(sql);
}
