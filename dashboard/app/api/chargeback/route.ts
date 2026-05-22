/**
 * Server-side proxy for ClickHouse chargeback queries.
 * Keeps ClickHouse credentials out of the browser.
 *
 * GET /api/chargeback?days=30
 *
 * Returns raw joules so the client applies PUE and cost/kWh without re-fetching.
 */

import { NextRequest, NextResponse } from "next/server";
import { queryChargeback } from "@/lib/clickhouse";

export async function GET(req: NextRequest): Promise<NextResponse> {
  const daysParam = req.nextUrl.searchParams.get("days");
  const days = daysParam ? parseInt(daysParam, 10) : 30;

  if (isNaN(days) || days < 1 || days > 365) {
    return NextResponse.json(
      { error: "days must be an integer between 1 and 365" },
      { status: 400 },
    );
  }

  try {
    const rows = await queryChargeback(days);
    return NextResponse.json({ rows });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
