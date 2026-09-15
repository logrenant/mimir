import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { api, DaemonError, type Account, type AccountStatus } from "../lib/daemon";

/**
 * The one Claude account Mimir is connected as, and who it is.
 *
 * There is at most one, and it outlives the app: the credential slot is Mimir's
 * own and the keychain keeps the login, so "connected" is the normal state at a
 * launch and the operator signs in once. The daemon still reconciles the slot
 * against the keychain when it starts, so "not connected" means a login that
 * was never made or has been signed out — not merely that the app was closed.
 * Nothing runs without it — the daemon refuses a task with no identity to
 * spend — so this is the first thing a screen has to be able to say.
 *
 * The probe is what turns a slot into a name. It is free — `claude auth status`
 * reads a keychain entry and prints JSON, no API call — but it is a subprocess,
 * so it runs when the account appears rather than on a timer. `refresh` re-runs
 * it, which is what to call after a login or a sign-out.
 *
 * One provider rather than a hook per screen: the dashboard, the workspace and
 * the composer all ask the same question, and separate fetches meant they could
 * disagree about whether anything was connected.
 */

type AccountsAPI = {
  /** Null means nothing is connected, which now means signed out rather than freshly launched. */
  account: Account | null;
  /** False until the first list has come back, so a screen can wait rather than flash "no account". */
  loaded: boolean;
  /** Null means "not probed yet", not "signed out". */
  status: AccountStatus | null;
  error: string | null;
  refresh: () => void;
};

const AccountsContext = createContext<AccountsAPI | null>(null);

export function useAccounts(): AccountsAPI {
  const ctx = useContext(AccountsContext);
  if (!ctx) throw new Error("useAccounts must be used inside <AccountsProvider>");
  return ctx;
}

/** The identity behind the slot, as a human reads it. Empty when unknown. */
export function identityLabel(status: AccountStatus | null | undefined): string {
  if (!status) return "";
  if (status.error) return status.error;
  if (!status.logged_in) return "giriş yok";
  return status.email ?? "?";
}

export function AccountsProvider({ children }: { children: ReactNode }) {
  const [account, setAccount] = useState<Account | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [status, setStatus] = useState<AccountStatus | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const { accounts } = await api.listAccounts();
      setAccount(accounts[0] ?? null);
      setError(null);
    } catch (err) {
      setAccount(null);
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setLoaded(true);
    }
  }, []);

  const refresh = useCallback(() => {
    // A refresh is what runs after a login or a sign-out, so the cached
    // identity is exactly what must not survive it.
    setStatus(null);
    void load();
  }, [load]);

  useEffect(() => {
    void load();
  }, [load]);

  const id = account?.id ?? null;
  useEffect(() => {
    if (!id) {
      setStatus(null);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const probed = await api.accountStatus(id);
        if (!cancelled) setStatus(probed);
      } catch (err) {
        // A slot that cannot be read is an answer about the slot, not a
        // failure of this component: it is exactly the state worth showing in
        // red.
        if (!cancelled) {
          setStatus({
            logged_in: false,
            error: err instanceof DaemonError ? err.message : String(err),
          });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  const value = useMemo<AccountsAPI>(
    () => ({ account, loaded, status, error, refresh }),
    [account, loaded, status, error, refresh],
  );

  return <AccountsContext.Provider value={value}>{children}</AccountsContext.Provider>;
}
