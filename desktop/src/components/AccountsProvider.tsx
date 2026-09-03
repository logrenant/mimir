import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api, DaemonError, type Account, type AccountStatus } from "../lib/daemon";

/**
 * The registered credential slots, and who each one currently is.
 *
 * Two things are held together here because they are only useful together. A
 * slot is a directory the CLI hashes into a keychain entry name — it carries no
 * identity of its own, so "ikinci" or "b" on a picker says nothing about which
 * account will pay for the run. The operator chooses by account, not by
 * directory: they watch two credit balances and pick the one with room.
 *
 * The probe is what turns a directory into a name. It is free — `claude auth
 * status` reads a keychain entry and prints JSON, no API call — but it is a
 * subprocess, so it runs once per slot per app session rather than on a timer.
 * `refresh` re-runs it, which is what to call after a login.
 *
 * One provider rather than a hook per screen: four call sites each fetching
 * `/accounts` on mount, and the manager probing separately from everything
 * else, meant the picker and the manager could disagree about the same slot.
 */

type AccountsAPI = {
  accounts: Account[] | null;
  /** Keyed by account id. Absent means "not probed yet", not "signed out". */
  statuses: Record<string, AccountStatus>;
  error: string | null;
  /** Rescans the accounts directory, re-reads the list, re-probes every slot. */
  refresh: () => void;
};

const AccountsContext = createContext<AccountsAPI | null>(null);

export function useAccounts(): AccountsAPI {
  const ctx = useContext(AccountsContext);
  if (!ctx) throw new Error("useAccounts must be used inside <AccountsProvider>");
  return ctx;
}

/** The identity behind a slot, as a human reads it. Empty when unknown. */
export function identityLabel(status: AccountStatus | undefined): string {
  if (!status) return "";
  if (status.error) return status.error;
  if (!status.logged_in) return "giriş yok";
  return status.email ?? "?";
}

export function AccountsProvider({ children }: { children: ReactNode }) {
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [statuses, setStatuses] = useState<Record<string, AccountStatus>>({});
  const [error, setError] = useState<string | null>(null);

  // Slots already probed this session. A ref because it gates an effect that
  // would otherwise re-run itself by writing the state it reads.
  const probed = useRef(new Set<string>());

  const probe = useCallback(async (id: string) => {
    try {
      const status = await api.accountStatus(id);
      setStatuses((current) => ({ ...current, [id]: status }));
    } catch (err) {
      // An unreadable slot is an answer about the slot, not a failure of this
      // component: it is exactly the state worth showing in red.
      setStatuses((current) => ({
        ...current,
        [id]: { logged_in: false, error: err instanceof DaemonError ? err.message : String(err) },
      }));
    }
  }, []);

  const load = useCallback(async () => {
    try {
      const { accounts: list } = await api.listAccounts();
      setAccounts(list);
      setError(null);
    } catch (err) {
      setAccounts([]);
      setError(err instanceof DaemonError ? err.message : String(err));
    }
  }, []);

  const refresh = useCallback(() => {
    // A refresh is what an operator clicks after logging a slot in, so the
    // cached identities are exactly what must not survive it.
    probed.current.clear();
    setStatuses({});
    // And it is also what they click after *creating* a slot, so the scan goes
    // first: the daemon reads the accounts directory at startup, and a
    // directory made since then would otherwise need a restart to appear. A
    // failed scan is not a failed refresh — the list still loads below, and
    // load() reports whatever went wrong with that.
    void api
      .scanAccounts()
      .catch(() => undefined)
      .then(load);
  }, [load]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    for (const account of accounts ?? []) {
      if (probed.current.has(account.id)) continue;
      probed.current.add(account.id);
      void probe(account.id);
    }
  }, [accounts, probe]);

  const value = useMemo<AccountsAPI>(
    () => ({ accounts, statuses, error, refresh }),
    [accounts, statuses, error, refresh],
  );

  return <AccountsContext.Provider value={value}>{children}</AccountsContext.Provider>;
}
