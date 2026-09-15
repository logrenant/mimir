import { useCallback, useEffect, useRef, useState } from "react";
import { api, DaemonError, type LoginState } from "../lib/daemon";
import { Button, IconButton } from "./ui/button";
import { Input } from "./ui/field";
import { Icon } from "./ui/icon";
import { Popover, PopoverBody, PopoverHead } from "./ui/popover";
import { useAccounts } from "./AccountsProvider";

/**
 * Connecting and disconnecting Mimir's one Claude account.
 *
 * The account is Mimir's own credential slot, not the operator's: the CLI
 * derives its keychain entry from a directory path, and the path Mimir hands it
 * is one Mimir made for itself. The `claude` in their own terminal is a
 * different slot and is never touched by anything on this panel.
 *
 * Connecting is a one-time act. The slot survives a quit — the keychain keeps
 * the login and the daemon reconciles it at the next launch — so this panel is
 * usually just reporting who is connected. "bağlan" runs `claude auth login` on
 * the daemon and opens the authorization page in a *private* window, because a
 * normal one carries whatever Claude session the browser already has and would
 * never ask which account is connecting.
 *
 * "çıkış yap" is therefore the deliberate act, not the incidental one: it is
 * both signing out and the only way to switch to a different account.
 *
 * Everything the daemon says is shown as written, including the CLI's own
 * output — it is already written for a human, and paraphrasing a login error
 * into "bir şeyler ters gitti" is how an operator ends up with no idea which
 * step failed.
 *
 * ---------------------------------------------------------------------------
 * What this panel stopped doing.
 * ---------------------------------------------------------------------------
 * Two things, and both were the same mistake: saying at all times what only
 * matters once.
 *
 * It drew the connected identity in a bordered row — directly under the *same*
 * identity, which its container on the dashboard had already drawn with an orb
 * and a load meter. Two rows, one fact. This panel no longer reports who is
 * connected; it offers the two acts that change who is connected, and the
 * reporting belongs to whoever placed it.
 *
 * And it ended with an eight-line paragraph explaining the private Chrome
 * window, the Keychain, `claude auth logout` and how to switch accounts —
 * permanently, on the first screen the app opens, for something an operator
 * does roughly once. It is behind a question mark now. The text is unchanged
 * and every word of it is still true; what changed is that it is read when it
 * is wanted rather than skipped every day.
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

  // Signing out and straight back in, as one action. Two separate clicks would
  // work, but the connect button is hidden while a row exists — and the row is
  // exactly what is still there when a login has lapsed.
  const reconnect = useCallback(async () => {
    setFailure(null);
    setBusy(true);
    try {
      await api.resetAccounts();
      setLogin(await api.startAccountLogin());
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
    <div className="flex flex-col gap-3">
      {account ? (
        <div className="flex flex-wrap items-center gap-2">
          {/* A row whose login has lapsed is the one state persistence adds:
              the slot is connected as far as the store is concerned, but the
              keychain entry behind it is dead, so the connect button is hidden
              and "çıkış yap" alone would leave the operator to work out that
              they have to sign out before they can sign back in. */}
          {status && !status.logged_in && (
            <Button
              variant="ghost"
              size="sm"
              icon="refresh"
              disabled={busy}
              onClick={() => void reconnect()}
            >
              Yeniden bağlan
            </Button>
          )}
          <Button variant="quiet" size="sm" disabled={busy} onClick={() => void disconnect()}>
            Çıkış yap
          </Button>
          <div className="flex-1" />
          <HowItWorks />
        </div>
      ) : (
        loaded && (
          <div className="flex flex-col gap-3">
            <p className="text-sm leading-[1.6] text-muted">
              Bağlı hesap yok. Bir kez bağlanmanız yeterli — yuva uygulama
              kapansa da duruyor.
            </p>
            {!inFlight.has(state) && (
              <div className="flex items-center gap-2">
                <Button size="sm" icon="user" disabled={busy} onClick={() => void connect()}>
                  {state === "failed" ? "Yeniden dene" : "Anthropic hesabı bağla"}
                </Button>
                <div className="flex-1" />
                <HowItWorks />
              </div>
            )}
          </div>
        )
      )}

      {state !== "idle" && state !== "done" && (
        <div className="flex flex-col gap-2.5 rounded-md bg-sunken px-3.5 py-3">
          <p className="text-sm leading-[1.6]">
            {state === "opening" && "Giriş başlatılıyor…"}
            {state === "waiting" &&
              "Tarayıcıdaki gizli pencerede giriş yapın — burası bekliyor. Sayfa size bir kod verdiyse aşağıya yapıştırın."}
            {state === "code" &&
              "CLI tarayıcıyı kendisi açamadı ve bir kod istiyor: sayfadaki kodu aşağıya yapıştırın."}
            {state === "failed" && "Giriş tamamlanmadı."}
          </p>
          {login?.message && <p className="text-xs text-muted">{login.message}</p>}
          {login?.url && (
            <p className="text-xs break-all text-muted">
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
              <Input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="sayfadaki kod"
                className="w-40"
              />
              <Button
                variant="ghost"
                size="sm"
                disabled={busy || !code.trim()}
                onClick={() => void sendCode()}
              >
                Gönder
              </Button>
            </div>
          )}
          {login?.output && (
            <pre className="max-h-32 overflow-auto font-mono text-xs leading-relaxed whitespace-pre-wrap text-muted">
              {login.output}
            </pre>
          )}
        </div>
      )}

      {(failure || error) && (
        <p className="flex items-start gap-1.5 font-mono text-xs leading-[1.5] text-bad">
          <Icon name="alert" size={13} className="mt-px shrink-0" />
          {failure ?? error}
        </p>
      )}
    </div>
  );
}

/**
 * The eight lines that used to be printed under this panel at all times.
 *
 * Every word is still here and none of it is paraphrased — it explains the
 * private window, where the credential actually lives, what "çıkış yap" does to
 * the slot, and how to switch accounts. It is a genuinely useful paragraph, and
 * it is read roughly once, which is exactly the profile of something that
 * belongs behind a control rather than in front of the operator every day.
 */
function HowItWorks() {
  const trigger = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);

  return (
    <>
      <IconButton
        ref={trigger}
        name="info"
        label="Hesap bağlama nasıl çalışır?"
        size="sm"
        aria-haspopup="dialog"
        active={open}
        activeAria="expanded"
        onClick={() => setOpen((was) => !was)}
      />
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={trigger}
        align="end"
        width={330}
        label="Hesap bağlama nasıl çalışır?"
      >
        <PopoverHead title="Hesap nasıl bağlanır" />
        <PopoverBody>
          <p className="text-sm leading-[1.7] text-muted">
            Bağlanmak Chrome'da gizli bir pencerede Anthropic giriş sayfasını
            açar; giriş bilgisi macOS Keychain'de kalır, Mimir görmez ve
            saklamaz. Oturum uygulamayı kapatınca kapanmaz — kapatan tek şey{" "}
            <code className="text-mist">çıkış yap</code>, o da bu yuvayı{" "}
            <code className="text-mist">claude auth logout</code> ile kapatıp
            kaydı siler. Başka bir Anthropic hesabına geçmenin yolu da bu: çıkış
            yapın, diğer hesapla bağlanın. Aynı anda tek hesap, tek job.
          </p>
          <p className="mt-3 text-sm leading-[1.7] text-muted">
            Terminaldeki kendi <code className="text-mist">claude</code>{" "}
            oturumunuz ayrı bir yuva; ona dokunulmuyor.
          </p>
        </PopoverBody>
      </Popover>
    </>
  );
}
