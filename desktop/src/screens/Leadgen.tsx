import { useState } from "react";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import {
  api,
  DaemonError,
  type CategoryReport,
  type EmailStatus,
  type LeadCompany,
  type LeadgenReport,
} from "../lib/daemon";

/**
 * The Maps lead-gen pipeline, one screen.
 *
 * A region search (always) plus two opt-in stages that spend Claude tokens:
 * category gap analysis, and one outreach email per company. The daemon owns
 * every cost and caching decision — this screen only collects the request,
 * renders the Report, and lets a human mark an email sent or skipped.
 */
export function Leadgen() {
  const [query, setQuery] = useState("");
  const [region, setRegion] = useState("");
  const [count, setCount] = useState("");
  const [withGaps, setWithGaps] = useState(true);
  const [withEmails, setWithEmails] = useState(false);

  const [report, setReport] = useState<LeadgenReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // An email needs its category's gap analysis, so the daemon forces gaps on
  // when emails are requested. Mirror that here so the checkbox does not lie.
  const gapsEffective = withGaps || withEmails;

  const run = async () => {
    if (!query.trim()) return;
    setError(null);
    setBusy(true);
    setReport(null);
    try {
      const parsedCount = Number.parseInt(count, 10);
      const result = await api.runLeadgen({
        query: query.trim(),
        region: region.trim() || undefined,
        count: Number.isFinite(parsedCount) && parsedCount > 0 ? parsedCount : undefined,
        gap_analysis: gapsEffective,
        emails: withEmails,
      });
      setReport(result);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const onStatusChange = (placeID: string, status: EmailStatus) => {
    setReport((current) => {
      if (!current) return current;
      return {
        ...current,
        companies: current.companies.map((c) =>
          c.place_id === placeID ? { ...c, email_status: status } : c,
        ),
      };
    });
  };

  return (
    <div className="mx-auto grid h-full max-w-5xl grid-rows-[auto_1fr] gap-4 p-6">
      <Card>
        <CardHeader
          title="Lead-gen"
          subtitle="Region search → categorize → gap analysis → outreach email"
        />
        <CardBody className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-[2fr_1fr_auto]">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="What you would type into Google Maps, e.g. 'dentists in Kadıköy, Istanbul'"
              className="rounded border border-edge bg-ink px-3 py-2 text-sm outline-none focus:border-accent/60"
            />
            <input
              value={region}
              onChange={(e) => setRegion(e.target.value)}
              placeholder="Region label (optional)"
              className="rounded border border-edge bg-ink px-3 py-2 text-sm outline-none focus:border-accent/60"
            />
            <input
              value={count}
              onChange={(e) => setCount(e.target.value.replace(/[^0-9]/g, ""))}
              inputMode="numeric"
              placeholder="Count"
              className="w-20 rounded border border-edge bg-ink px-3 py-2 text-sm outline-none focus:border-accent/60"
            />
          </div>

          <div className="flex flex-wrap items-center gap-4 text-sm">
            <label className="flex cursor-pointer items-center gap-2">
              <input
                type="checkbox"
                className="accent-accent"
                checked={gapsEffective}
                disabled={withEmails}
                onChange={(e) => setWithGaps(e.target.checked)}
              />
              Analyze category gaps
            </label>
            <label className="flex cursor-pointer items-center gap-2">
              <input
                type="checkbox"
                className="accent-accent"
                checked={withEmails}
                onChange={(e) => setWithEmails(e.target.checked)}
              />
              Draft outreach emails
            </label>
            <span className="text-xs text-muted">
              gaps and emails spend Claude tokens; results are cached per region
            </span>
          </div>

          <div className="flex items-center gap-3">
            <Button onClick={() => void run()} disabled={busy || !query.trim()}>
              {busy ? "Running…" : "Run"}
            </Button>
            {error && <span className="text-sm text-bad">{error}</span>}
          </div>
        </CardBody>
      </Card>

      {report ? (
        <ReportView report={report} onStatusChange={onStatusChange} />
      ) : (
        <Card className="grid place-items-center">
          <p className="p-8 text-sm text-muted">
            Run a search to see companies, their categories, and — if requested — a gap analysis
            per category and a drafted email per company.
          </p>
        </Card>
      )}
    </div>
  );
}

function ReportView({
  report,
  onStatusChange,
}: {
  report: LeadgenReport;
  onStatusChange: (placeID: string, status: EmailStatus) => void;
}) {
  const stages = [
    report.ran_categorize && "categorized",
    report.ran_gap_analysis && "gap analysis",
    report.ran_emails && "emails",
  ].filter(Boolean) as string[];

  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader
        title="Results"
        subtitle={`${report.companies.length} companies · ${report.region}`}
        aside={
          <div className="flex items-center gap-2">
            {report.from_cache && <Badge tone="muted">from cache</Badge>}
            {stages.map((s) => (
              <Badge key={s} tone="ok">
                {s}
              </Badge>
            ))}
          </div>
        }
      />
      <CardBody className="min-h-0 flex-1 space-y-4 overflow-auto">
        {report.categories.length > 0 && (
          <section className="space-y-2">
            {report.categories.map((c) => (
              <CategoryCard key={c.category} report={c} />
            ))}
          </section>
        )}

        <section className="space-y-2">
          {report.companies.map((company) => (
            <CompanyRow key={company.place_id || company.name} company={company} onStatusChange={onStatusChange} />
          ))}
        </section>

        {report.notes && report.notes.length > 0 && (
          <details className="rounded border border-edge bg-ink/60 p-3">
            <summary className="cursor-pointer text-xs text-muted">
              {report.notes.length} note{report.notes.length === 1 ? "" : "s"} from the run
            </summary>
            <ul className="mt-2 space-y-1 text-xs text-muted">
              {report.notes.map((note, i) => (
                <li key={i}>{note}</li>
              ))}
            </ul>
          </details>
        )}
      </CardBody>
    </Card>
  );
}

function CategoryCard({ report }: { report: CategoryReport }) {
  return (
    <div className="rounded border border-edge bg-ink/60 p-3">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">{report.category}</span>
        <span className="text-xs text-muted">{report.company_count} companies</span>
        {report.gap_method && <Badge tone="muted">{report.gap_method}</Badge>}
        {report.truncated && <Badge tone="warn">truncated</Badge>}
      </div>
      {report.gap_analysis && (
        <pre className="mt-2 whitespace-pre-wrap text-xs leading-relaxed text-muted">
          {report.gap_analysis}
        </pre>
      )}
    </div>
  );
}

function CompanyRow({
  company,
  onStatusChange,
}: {
  company: LeadCompany;
  onStatusChange: (placeID: string, status: EmailStatus) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [rowError, setRowError] = useState<string | null>(null);

  const mark = async (status: EmailStatus) => {
    if (!company.place_id) return;
    setBusy(true);
    setRowError(null);
    try {
      await api.setEmailStatus(company.place_id, status);
      onStatusChange(company.place_id, status);
    } catch (err) {
      setRowError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const statusTone = company.email_status === "sent" ? "ok" : company.email_status === "skipped" ? "muted" : "warn";

  return (
    <div className="rounded border border-edge bg-ink/60">
      <div className="flex flex-wrap items-center gap-2 px-3 py-2">
        <span className="text-sm font-medium">{company.name}</span>
        <Badge tone="muted">{company.category}</Badge>
        {company.rating ? (
          <span className="text-xs text-muted">
            {company.rating.toFixed(1)}★ ({company.review_count ?? 0})
          </span>
        ) : null}
        {!company.website && <Badge tone="warn">no website</Badge>}
        {company.email_status && <Badge tone={statusTone}>{company.email_status}</Badge>}
        <span className="ml-auto truncate text-xs text-muted" title={company.address}>
          {company.address}
        </span>
      </div>

      {company.email && (
        <details className="border-t border-edge">
          <summary className="cursor-pointer px-3 py-2 text-xs text-muted">Draft email</summary>
          <div className="space-y-2 px-3 pb-3">
            <pre className="whitespace-pre-wrap text-xs leading-relaxed">{company.email}</pre>
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                onClick={() => void mark("sent")}
                disabled={busy || company.email_status === "sent"}
              >
                Mark sent
              </Button>
              <Button
                variant="ghost"
                onClick={() => void mark("skipped")}
                disabled={busy || company.email_status === "skipped"}
              >
                Skip
              </Button>
              {rowError && <span className="text-xs text-bad">{rowError}</span>}
            </div>
          </div>
        </details>
      )}
    </div>
  );
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
