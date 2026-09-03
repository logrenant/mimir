import { useState } from "react";
import { open } from "@tauri-apps/plugin-dialog";
import { api, DaemonError, type Account, type AccountStatus } from "../lib/daemon";
import { identityClashes } from "../lib/accounts";
import { identityLabel, useAccounts } from "./AccountsProvider";

/**
 * Which Claude Code identity a run spends.
 *
 * An account is a credential slot, not a login: the CLI derives its keychain
 * entry from a directory path, so registering one is pointing at a directory —
 * the same shape as registering a project, and the path leaves the app exactly
 * once. Mimir never sees a credential.
 *
 * Capacity is one run per slot. That is not a setting: two runs sharing an
 * identity share its rate limit and its session state, so the second is
 * contention rather than throughput. Registering a second account is how you
 * get two at a time.
 */

/**
 * The select a composer shows. "Otomatik" is the default and stays first.
 *
 * Each row names the identity, not the directory, when the probe has come
 * back: the operator picks by account — they watch two credit balances and
 * take the one with room — and "b" tells them nothing about which one that is.
 */
export function AccountSelect({
  accounts,
  statuses,
  value,
  onChange,
  disabled,
}: {
  accounts: Account[] | null;
  statuses?: Record<string, AccountStatus>;
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  return (
    <select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      style={{
        background: "#101114",
        border: "1px solid #24272d",
        borderRadius: 6,
        padding: "7px 9px",
        font: "450 12px/1 ui-sans-serif,system-ui",
        color: "#eef0f2",
        outline: "none",
      }}
    >
      <option value="">Otomatik — ilk boşalan hesap</option>
      {(accounts ?? []).map((a) => {
        const who = identityLabel(statuses?.[a.id]);
        return (
          <option key={a.id} value={a.id}>
            {a.label}
            {who && who !== a.label ? ` — ${who}` : ""}
          </option>
        );
      })}
    </select>
  );
}

/** Registering and forgetting slots. The probes are the provider's. */
export function AccountManager({
  accounts,
  onChanged,
}: {
  accounts: Account[] | null;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { statuses } = useAccounts();

  const add = async () => {
    setError(null);
    setBusy(true);
    try {
      const picked = await open({
        directory: true,
        multiple: false,
        title: "Bu hesap için bir dizin seçin (boş olabilir)",
      });
      if (typeof picked !== "string") return;
      await api.registerAccount("", picked);
      onChanged();
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const addDefault = async () => {
    setError(null);
    setBusy(true);
    try {
      await api.registerAccount("Default", "");
      onChanged();
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // The slot the daemon's own model calls spend — refine, distil, recap. Those
  // are not coding runs: nothing dispatched them, so until one is marked they
  // go to whichever identity the daemon inherited. Clicking the marked one
  // again clears it, which means the CLI's own slot.
  const markBackground = async (id: string) => {
    setError(null);
    try {
      await api.setBackgroundAccount(id);
      onChanged();
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    }
  };

  const forget = async (id: string) => {
    setError(null);
    try {
      await api.deleteAccount(id);
      onChanged();
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    }
  };

  const hasDefault = (accounts ?? []).some((a) => a.is_default);

  // Two slots signed into the same account is the failure this whole screen
  // exists to prevent, and it is invisible from the rows: both are green, both
  // name a real keychain entry, and they share one rate limit.
  const clashes = identityClashes(accounts, statuses);

  return (
    <div className="space-y-3">
      {accounts !== null && accounts.length === 0 && (
        <p className="text-sm text-muted">
          Hesap yok — <code className="text-mist">~/.claude-accounts</code> taraması boş döndü ve
          çalıştırmalar CLI'ın varsayılan oturumunu kullanıyor, aynı anda yalnız biri
          çalışabiliyor. İkinci bir hesap eklemek iki işi paralel yürütür.
        </p>
      )}

      <div className="space-y-1">
        {(accounts ?? []).map((account) => {
          const status = statuses[account.id];
          return (
            <div
              key={account.id}
              className="flex items-center gap-3 rounded border border-edge px-3 py-2"
            >
              <span
                className="size-1.5 shrink-0 rounded-full"
                style={{
                  background: !status ? "#3b3f48" : status.logged_in ? "#c6f04a" : "#e5484d",
                }}
              />
              <span className="text-sm">{account.label}</span>
              <span className="truncate text-xs text-muted">
                {status?.error
                  ? status.error
                  : status
                    ? `${status.email ?? "?"}${status.subscription_type ? ` · ${status.subscription_type}` : ""}`
                    : "sorgulanıyor…"}
              </span>
              <span className="flex-1" />
              <span
                className="truncate text-xs text-muted"
                title={account.config_dir || "CLI'ın varsayılan kimlik yuvası"}
              >
                {account.is_default ? "varsayılan yuva" : account.config_dir}
              </span>
              <button
                type="button"
                onClick={() => void markBackground(account.is_background ? "" : account.id)}
                title="Daemon'un kendi model çağrıları (refine, distill, recap) bu hesabı harcasın"
                className={
                  account.is_background
                    ? "cursor-pointer rounded border border-edge bg-transparent px-2 py-0.5 text-xs text-mist"
                    : "cursor-pointer border-none bg-transparent text-xs text-muted hover:text-mist"
                }
              >
                arka plan
              </button>
              {/* A discovered slot belongs to the accounts directory, not to
                  this screen: forgetting it here would be undone by the next
                  scan, so the honest control is the directory itself. */}
              {account.discovered ? (
                <span className="text-xs text-muted" title="~/.claude-accounts taramasından geldi">
                  taramadan
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => void forget(account.id)}
                  className="cursor-pointer border-none bg-transparent text-xs text-muted hover:text-mist"
                >
                  unut
                </button>
              )}
            </div>
          );
        })}
      </div>

      {clashes.map((clash) => (
        <p key={clash.email} className="text-xs leading-relaxed text-warn">
          <strong>{clash.labels.join(" ve ")}</strong> aynı hesaba bağlı ({clash.email}). Ayrı
          birer Keychain yuvası ama tek kimlik: aynı rate limit'i paylaşırlar, yani bu iki yuva
          bir şerit eder, iki değil. Ayırmak için birine ikinci hesapla giriş yapın:{" "}
          <code className="text-mist">
            CLAUDE_SECURESTORAGE_CONFIG_DIR=&lt;dizin&gt; claude auth login
          </code>
        </p>
      ))}

      <div className="flex flex-wrap items-center gap-3">
        <button
          type="button"
          disabled={busy}
          onClick={() => void add()}
          className="cursor-pointer rounded border border-edge px-2.5 py-1 text-xs text-muted hover:text-mist"
        >
          Hesap ekle…
        </button>
        {!hasDefault && (
          <button
            type="button"
            disabled={busy}
            onClick={() => void addDefault()}
            className="cursor-pointer rounded border border-edge px-2.5 py-1 text-xs text-muted hover:text-mist"
          >
            Varsayılan oturumu ekle
          </button>
        )}
        {error && <span className="text-sm text-bad">{error}</span>}
      </div>

      <p className="text-xs text-muted">
        Bir hesap = bir dizin. CLI o yolu hash'leyip macOS Keychain'de ayrı bir kimlik yuvası
        kullanıyor; giriş bilgisi Keychain'de kalır, Mimir görmez.{" "}
        <code className="text-mist">~/.claude-accounts</code> altındaki her dizin açılışta
        taranıp buraya düşer — terminaldeki <code className="text-mist">claude-acct</code> ile aynı
        yuvalar. Yeni bir yuva: dizini oluşturup{" "}
        <code className="text-mist">CLAUDE_SECURESTORAGE_CONFIG_DIR=&lt;dizin&gt; claude auth login</code>
        , sonra yenile.
      </p>
    </div>
  );
}
