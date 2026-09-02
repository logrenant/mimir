import type { Account, AccountStatus } from "./daemon";

/**
 * Reading the live probe results as a group.
 *
 * One slot at a time is not the interesting question — the whole point of a
 * second account is that it is a *second identity*, and nothing in the shape of
 * a slot guarantees that. A slot is a directory the CLI hashes into a keychain
 * entry name; registering one and signing it into the account you already use
 * produces two entries, two green rows, and one rate limit.
 */

export type IdentityClash = {
  email: string;
  /** Labels of the slots that turned out to be the same person. */
  labels: string[];
};

/**
 * Registered slots that resolve to the same account.
 *
 * Compared by email because that is what the operator recognises; `orgId`
 * would also collide for two personal accounts in the same org, which is not
 * the failure being described here.
 */
export function identityClashes(
  accounts: Account[] | null,
  statuses: Record<string, AccountStatus>,
): IdentityClash[] {
  const byEmail = new Map<string, string[]>();

  for (const account of accounts ?? []) {
    const status = statuses[account.id];
    // A slot that is not signed in, or that could not be read, is not evidence
    // of anything: it has no identity to collide with yet.
    if (!status?.logged_in) continue;
    const email = status.email?.trim();
    if (!email) continue;
    byEmail.set(email, [...(byEmail.get(email) ?? []), account.label || account.id]);
  }

  return [...byEmail.entries()]
    .filter(([, labels]) => labels.length > 1)
    .map(([email, labels]) => ({ email, labels }));
}

/**
 * How many identities the operator can actually run in parallel.
 *
 * This is the number the dispatcher's capacity is really worth: two slots on
 * one account are one lane, not two, because they share the rate limit and the
 * session state the runner is trying to keep apart.
 */
export function distinctIdentities(
  accounts: Account[] | null,
  statuses: Record<string, AccountStatus>,
): number {
  const emails = new Set<string>();
  for (const account of accounts ?? []) {
    const status = statuses[account.id];
    if (!status?.logged_in) continue;
    const email = status.email?.trim();
    if (email) emails.add(email);
  }
  return emails.size;
}
