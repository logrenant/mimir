import { useCallback, useEffect, useState } from "react";
import { api, DaemonError, type LoginState } from "../lib/daemon";
import { useAccounts } from "./AccountsProvider";

/**
 * Connecting and disconnecting Mimir's one Claude account.
 *
 * The account is Mimir's own credential slot, not the operator's: the CLI
 * derives its keychain entry from a directory path, and the path Mimir hands it
 * is one Mimir made for itself. That is what makes signing out on quit safe —
 * the `claude` in their own terminal is a different slot and keeps its login.
 *
 * So this is a login flow, which the old multi-slot manager deliberately was
 * not. There is nowhere to point at a directory any more, and nothing to forget
 * without signing out: "bağlan" runs `claude auth login` on the daemon and
 * opens the authorization page in a *private* window, because a normal one
 * carries whatever Claude session the browser already has and would never ask
 * which account is connecting.
 *
 * Everything the daemon says is shown as written, including the CLI's own
 * output — it is already written for a human, and paraphrasing a login error
 * into "bir şeyler ters gitti" is how an operator ends up with no idea which
 * step failed.
 */

/** States in which the attempt is still going and worth polling. */
const inFlight = new Set<LoginState["state"]>(["opening", "waiting", "code"]);

const describe = (err: unknown) => (err instanceof DaemonError ? err.message : String(err));

export function AccountPanel() {
  const { account, loaded, status, error, refresh } = useAccounts();
  const [login, setLogin] = useState<LoginState | null>(null);
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState("");
  const [failure, setFailure] = useState<string | null>(null);

  // An attempt may have been started before this screen was mounted — the flow
  // lives on the daemon, not here, so the panel joins one in progress rather
  // than offering to start a second.
  useEffect(() => {
    void api
      .loginState()
      .then(setLogin)
      .catch(() => undefined);
  }, []);

  const state = login?.state ?? "idle";

  useEffect(() => {
    if (!inFlight.has(state)) return;
    const id = window.setInterval(() => {
      void api
        .loginState()
        .then((next) => {
          setLogin(next);
          // The account row appears the moment the login lands, and the probe
          // behind it is what names the identity.
          if (next.state === "done") refresh();
        })
        .catch(() => undefined);
    }, 2000);
    return () => window.clearInterval(id);
  }, [state, refresh]);

  const connect = useCallback(async () => {
    setFailure(null);
    setBusy(true);
    try {
      setLogin(await api.startAccountLogin());
    } catch (err) {
      setFailure(describe(err));
    } finally {
      setBusy(false);
    }
  }, []);

  const disconnect = useCallback(async () => {
    setFailure(null);
    setBusy(true);
    try {
      await api.resetAccounts();
      setLogin(null);
      refresh();
    } catch (err) {
      setFailure(describe(err));
    } finally {
      setBusy(false);
    }
  }, [refresh]);

  const sendCode = useCallback(async () => {
    setFailure(null);
    setBusy(true);
    try {
      await api.submitLoginCode(code);
      setCode("");
    } catch (err) {
      setFailure(describe(err));
    } finally {
      setBusy(false);
    }
  }, [code]);

  return (
    <div className="space-y-3">
      {account ? (
        <div className="flex items-center gap-3 rounded border border-edge px-3 py-2">
          <span
            className="size-1.5 shrink-0 rounded-full"
            style={{ background: !status ? "#3b3f48" : status.logged_in ? "#c6f04a" : "#e5484d" }}
          />
          <span className="truncate text-sm">
            {status?.error
              ? status.error
              : status
                ? `${status.email ?? "?"}${status.subscription_type ? ` · ${status.subscription_type}` : ""}`
                : "sorgulanıyor…"}
          </span>
          <span className="flex-1" />
          <button
            type="button"
            disabled={busy}
            onClick={() => void disconnect()}
            className="cursor-pointer border-none bg-transparent text-xs text-muted hover:text-mist"
          >
            çıkış yap
          </button>
        </div>
      ) : (
        loaded && (
          <p className="text-sm text-muted">
            Bağlı hesap yok. Mimir kendi kimlik yuvasını kullanıyor ve kapanırken oturumu
            kapatıyor, yani her açılışta yeniden bağlanmak gerekiyor — terminaldeki kendi{" "}
            <code className="text-mist">claude</code> oturumunuza dokunmamasının bedeli bu.
          </p>
        )
      )}

      {state !== "idle" && state !== "done" && (
        <div className="space-y-2 rounded border border-edge px-3 py-2">
          <p className="text-sm">
            {state === "opening" && "Giriş başlatılıyor…"}
            {state === "waiting" &&
              "Tarayıcıdaki gizli pencerede giriş yapın — burası bekliyor. Sayfa size bir kod verdiyse aşağıya yapıştırın."}
            {state === "code" &&
              "CLI tarayıcıyı kendisi açamadı ve bir kod istiyor: sayfadaki kodu aşağıya yapıştırın."}
            {state === "failed" && "Giriş tamamlanmadı."}
          </p>
          {login?.message && <p className="text-xs text-muted">{login.message}</p>}
          {login?.url && (
            <p className="break-all text-xs text-muted">
              Pencere görünmediyse bu adresi elle açın:{" "}
              <code className="text-mist">{login.url}</code>
            </p>
          )}
          {/* Also in `waiting`, not only in `code`. The CLI prints its paste
              prompt in wording this daemon has to recognise, and when it does
              not — or when the browser hands over a code while the callback is
              still being waited on — the operator was left holding a code with
              nowhere to type it. The daemon accepts one in both states. */}
          {(state === "code" || state === "waiting") && (
            <div className="flex flex-wrap items-center gap-2">
              <input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="sayfadaki kod"
                className="rounded border border-edge bg-transparent px-2 py-1 text-xs"
              />
              <button
                type="button"
                disabled={busy || !code.trim()}
                onClick={() => void sendCode()}
                className="cursor-pointer rounded border border-edge px-2.5 py-1 text-xs text-muted hover:text-mist"
              >
                Gönder
              </button>
            </div>
          )}
          {login?.output && (
            <pre className="max-h-32 overflow-auto whitespace-pre-wrap text-[11px] leading-relaxed text-muted">
              {login.output}
            </pre>
          )}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        {!account && !inFlight.has(state) && (
          <button
            type="button"
            disabled={busy}
            onClick={() => void connect()}
            className="cursor-pointer rounded border border-edge px-2.5 py-1 text-xs text-muted hover:text-mist"
          >
            {state === "failed" ? "Yeniden dene" : "Anthropic hesabı bağla"}
          </button>
        )}
        {(failure || error) && <span className="text-sm text-bad">{failure ?? error}</span>}
      </div>

      <p className="text-xs text-muted">
        Bağlanmak Chrome'da gizli bir pencerede Anthropic giriş sayfasını açar; giriş bilgisi
        macOS Keychain'de kalır, Mimir görmez. Mimir kapandığında bu yuva{" "}
        <code className="text-mist">claude auth logout</code> ile kapatılır ve kayıt silinir —
        aynı anda tek hesap, tek job.
      </p>
    </div>
  );
}
