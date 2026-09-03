//! Finds the `mimir-daemon` this app talks to — attaching to the installed
//! always-on one, or spawning its own in development.
//!
//! The parent owns the plumbing, and that is the whole design. goat v1 had its
//! shell scrape `MIMIR_PORT=<n>` out of the child's stdout; this repo made that
//! impossible on the child's side (SD-4 — stdout carries MCP frames and nothing
//! else, and the daemon logs its resolved address to stderr precisely because
//! the parent is supposed to already know it). Nothing in this file reads
//! either value back out of a pipe.
//!
//! There are now two legitimate parents, and exactly one of them is live at a
//! time:
//!
//! * **Attached (the installed system).** `scripts/install-agent.sh` registered
//!   `studio.mimir.daemon` with launchd, which owns the process, restarts it if it
//!   dies, and starts it at login. launchd is the parent; it was handed the
//!   port and the token at install time, and the same pair is in
//!   `endpoint.json`, `0600`, for this app to read. The daemon outlives the
//!   window, so this app never spawns and never kills it.
//! * **Spawned (development).** No `endpoint.json` means no agent is installed
//!   — `make desktop-dev` on a fresh checkout — so the shell falls back to the
//!   original behaviour: reserve a port, mint a per-launch token, run the
//!   sidecar as a child, and reap it on exit.
//!
//! The two modes are mutually exclusive on purpose. Two daemons against one
//! SQLite store would contend for its write lock, so the presence of the
//! endpoint file decides, and nothing races it.

use std::collections::HashMap;
use std::fs;
use std::io::Read;
use std::net::TcpListener;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager, State};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// The launchd job `scripts/install-agent.sh` registers.
const AGENT_LABEL: &str = "studio.mimir.daemon";

/// How long to wait for the child to answer /healthz before calling it dead.
const READY_TIMEOUT: Duration = Duration::from_secs(20);
const HEALTH_POLL_INTERVAL: Duration = Duration::from_millis(250);
/// A freshly-freed port can be taken by another process between the moment we
/// drop the listener and the moment the child binds it. Small window, real
/// race — so try again with a new port rather than reporting a broken install.
const PORT_ATTEMPTS: usize = 3;
/// Enough of the child's stderr to make a failure actionable, bounded so a
/// chatty or looping child cannot grow this without limit.
const STDERR_KEEP_LINES: usize = 40;

/// How long a plain daemon call may take before the shell gives up.
///
/// Two minutes, not the thirty seconds this used to be. The hop itself is
/// loopback and cannot be slow — but the daemon answers most of these routes by
/// going out to the network first (a search, a page fetch, a CLI subprocess
/// riding an existing login), so the budget here is really the budget for
/// *that*, and on a lossy link a TCP retransmit alone can eat the old ceiling.
const DEFAULT_REQUEST_TIMEOUT: Duration = Duration::from_secs(120);

/// The budget for routes that run a whole pipeline in one request.
///
/// A lead-gen run scrapes a region, classifies every company, then synthesizes
/// one gap analysis per category — minutes of work by design, and the client
/// holds the connection open for all of it. Cutting that off at the default
/// produced `could not reach the daemon: timeout: global` on a run that was
/// still going perfectly well on the other side, which is the worst kind of
/// error: the work completes, the answer is thrown away, and the screen says
/// the daemon is unreachable.
///
/// The ceiling still exists because a hung request must not become a spinner
/// nobody can clear. It is aligned with `CodingRunTimeout`, the longest thing
/// the daemon will do without streaming.
const PIPELINE_REQUEST_TIMEOUT: Duration = Duration::from_secs(45 * 60);

/// Connecting is a separate budget from answering, and it is short on purpose:
/// the daemon is on loopback, so a connect that does not complete promptly
/// means nothing is listening. Keeping this small is what lets the two budgets
/// above be generous without making a genuinely dead daemon look slow.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);

/// Routes that run a pipeline rather than a query, matched by prefix.
///
/// A list rather than a heuristic: "slow" is a property of what the handler
/// does, and the only honest source for that is the route table in
/// `internal/api/api.go`. A path that is not here gets the default, which is
/// the safe direction to be wrong in — a new slow route shows up as one
/// timeout, not as an unbounded hang.
const PIPELINE_ROUTES: &[&str] = &[
    "/maps/leadgen",
    "/brain/scan/now",
    "/brain/ingest",
    "/accounts/scan",
    "/research",
];

/// The budget for `path`.
fn request_timeout(path: &str) -> Duration {
    let route = path.split('?').next().unwrap_or(path);
    if PIPELINE_ROUTES
        .iter()
        .any(|p| route == *p || route.starts_with(&format!("{p}/")))
    {
        PIPELINE_REQUEST_TIMEOUT
    } else {
        DEFAULT_REQUEST_TIMEOUT
    }
}

/// What the WebView is told. The token is a live credential for a process that
/// runs coding sessions with file tools, so it travels over Tauri IPC and
/// nowhere else — never a URL, never a log line, never a window title.
#[derive(Clone, Serialize)]
pub struct Endpoint {
    pub base_url: String,
    pub token: String,
}

#[derive(Clone, Serialize)]
#[serde(tag = "state", rename_all = "snake_case")]
pub enum Status {
    Starting,
    Ready(Endpoint),
    Failed { message: String },
}

pub struct DaemonState {
    status: Mutex<Status>,
    child: Mutex<Option<CommandChild>>,
}

impl Default for DaemonState {
    fn default() -> Self {
        Self {
            status: Mutex::new(Status::Starting),
            child: Mutex::new(None),
        }
    }
}

impl DaemonState {
    fn set(&self, status: Status) {
        *self.status.lock().expect("daemon status poisoned") = status;
    }

    fn take_child(&self) -> Option<CommandChild> {
        self.child.lock().expect("daemon child poisoned").take()
    }
}

/// The IPC command. This is the only route by which the token reaches the
/// WebView.
#[tauri::command]
pub fn get_daemon_endpoint(state: State<'_, DaemonState>) -> Status {
    state.status.lock().expect("daemon status poisoned").clone()
}

/// The handshake state, for callers inside the shell (the tray's status line).
/// Same value `get_daemon_endpoint` hands the WebView, without the IPC hop.
pub fn status_snapshot(app: &AppHandle) -> Status {
    app.state::<DaemonState>()
        .status
        .lock()
        .expect("daemon status poisoned")
        .clone()
}

/// The version string a live daemon reports, or `None` if it does not answer.
///
/// Doubles as the tray's liveness check: an installed daemon can die and be
/// restarted by launchd while this app sits idle, so the status line asks the
/// process rather than trusting the handshake it did at start-up.
pub fn health_version(endpoint: &Endpoint) -> Option<String> {
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(2)))
        .build()
        .into();

    let mut response = agent
        .get(&format!("{}/healthz", endpoint.base_url))
        .header("Authorization", &format!("Bearer {}", endpoint.token))
        .call()
        .ok()?;

    let mut body = String::new();
    response
        .body_mut()
        .as_reader()
        .take(4096)
        .read_to_string(&mut body)
        .ok()?;

    if !body.contains("\"ok\":true") {
        return None;
    }
    let version: HealthzVersion = serde_json::from_str(&body).ok()?;
    Some(version.version)
}

#[derive(Deserialize)]
struct HealthzVersion {
    version: String,
}

/// One daemon reply, handed back to the WebView.
#[derive(Serialize)]
pub struct DaemonResponse {
    pub status: u16,
    pub body: String,
}

/// Performs a daemon REST call **from Rust**, on the WebView's behalf.
///
/// Not a convenience — the WebView cannot make this call itself. A `fetch` with
/// an `Authorization` header from `tauri://localhost` to `http://127.0.0.1:<p>`
/// is cross-origin, so the WebView sends a CORS preflight first; the daemon
/// answers `OPTIONS` with 401 because it has no CORS headers, deliberately
/// ("No CORS headers, ever" — internal/api/api.go). Adding them would hand any
/// web page that guesses the port the ability to read the replies, so the fix
/// is on this side: the browser is taken out of the HTTP path entirely.
///
/// It also means the token never reaches the WebView for any REST call. The
/// WebSocket still needs it (a browser socket cannot set headers, and the
/// handshake is exempt from preflight), which is the one remaining exposure.
///
/// SECURITY: `path` must stay a path. Without the check below this command is
/// an open proxy that attaches a live credential — the WebView could ask for
/// any host on the machine's network and read the answer.
#[tauri::command]
pub async fn daemon_request(
    app: AppHandle,
    method: String,
    path: String,
    body: Option<String>,
) -> Result<DaemonResponse, String> {
    if !path.starts_with('/') || path.starts_with("//") || path.contains("://") {
        return Err(format!(
            "refusing to proxy {path:?}: only daemon paths are allowed"
        ));
    }

    let endpoint = {
        let status = app.state::<DaemonState>();
        let guard = status.status.lock().expect("daemon status poisoned");
        match &*guard {
            Status::Ready(endpoint) => endpoint.clone(),
            Status::Starting => return Err("the daemon is still starting".to_string()),
            Status::Failed { message } => return Err(message.clone()),
        }
    };

    tauri::async_runtime::spawn_blocking(move || call_daemon(&endpoint, &method, &path, body))
        .await
        .map_err(|e| format!("daemon request panicked: {e}"))?
}

fn call_daemon(
    endpoint: &Endpoint,
    method: &str,
    path: &str,
    body: Option<String>,
) -> Result<DaemonResponse, String> {
    let budget = request_timeout(path);
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .timeout_global(Some(budget))
        .timeout_connect(Some(CONNECT_TIMEOUT))
        // A 4xx is an answer, not a transport failure: the daemon's error
        // envelope is the useful part and must reach the caller intact.
        .http_status_as_error(false)
        .build()
        .into();

    let url = format!("{}{}", endpoint.base_url, path);
    let auth = format!("Bearer {}", endpoint.token);

    // GET, POST and DELETE, because those are the only verbs the daemon's
    // routes answer to — DELETE since a board card can be thrown away. An
    // allowlist here rather than a pass-through keeps this from becoming a
    // general HTTP client the WebView can steer.
    let result = match (method.to_ascii_uppercase().as_str(), body) {
        ("GET", _) => agent.get(&url).header("Authorization", &auth).call(),
        ("POST", Some(payload)) => agent
            .post(&url)
            .header("Authorization", &auth)
            .header("Content-Type", "application/json")
            .send(payload),
        ("POST", None) => agent.post(&url).header("Authorization", &auth).send_empty(),
        ("DELETE", _) => agent.delete(&url).header("Authorization", &auth).call(),
        (other, _) => return Err(format!("unsupported method {other}")),
    };

    match result {
        Ok(mut response) => {
            let status = response.status().as_u16();
            let body = response.body_mut().read_to_string().unwrap_or_else(|e| {
                format!("{{\"error\":{{\"code\":\"unreadable\",\"message\":\"{e}\"}}}}")
            });
            Ok(DaemonResponse { status, body })
        }
        // `timeout: global` on its own says nothing an operator can act on —
        // not which call gave up, not how long it waited, not whether the
        // daemon was ever reached. Naming all three is the difference between
        // "the app is broken" and "that run needs longer than 45 minutes".
        Err(ureq::Error::Timeout(_)) => Err(format!(
            "the daemon did not answer {method} {path} within {}s — it may still be working; \
             give it a moment and look again before re-running",
            budget.as_secs()
        )),
        Err(err) => Err(format!("could not reach the daemon: {err}")),
    }
}

/// Retry after a failed start.
///
/// Attached: asks launchd to restart the job it owns, then re-runs the
/// handshake. Spawned: kills our child and starts a fresh one. Either way a
/// transient cause — a port that was briefly taken, a sidecar built a second
/// too late — does not need an app restart.
#[tauri::command]
pub fn restart_daemon(app: AppHandle) {
    let state = app.state::<DaemonState>();
    if let Some(child) = state.take_child() {
        let _ = child.kill();
    }
    if endpoint_file().exists() {
        let _ = kickstart_agent();
    }
    state.set(Status::Starting);
    start(app.clone());
}

/// Starts the supervisor on its own thread. Returns immediately: the UI shows
/// "starting" and asks again, rather than blocking the window's first paint on
/// a subprocess.
pub fn start(app: AppHandle) {
    std::thread::spawn(move || {
        let outcome = supervise(&app);
        let state = app.state::<DaemonState>();
        match outcome {
            Ok(endpoint) => state.set(Status::Ready(endpoint)),
            Err(message) => state.set(Status::Failed { message }),
        }
    });
}

fn supervise(app: &AppHandle) -> Result<Endpoint, String> {
    // Attached mode wins whenever the agent is installed: launchd owns that
    // process, and spawning a second one would put two writers on one SQLite
    // store.
    match read_endpoint(&endpoint_file()) {
        Ok(endpoint) => return attach(endpoint),
        Err(EndpointError::Missing) => {}
        Err(other) => return Err(other.to_string()),
    }

    spawn_supervised(app)
}

/// Connects to the daemon launchd is keeping alive.
///
/// A healthy daemon answers immediately. An unhealthy one is not ours to fix by
/// hand: launchd already restarts it, and `kickstart -k` is how you ask for
/// that now rather than in ten seconds' time.
fn attach(endpoint: Endpoint) -> Result<Endpoint, String> {
    if health_ok(&endpoint.base_url, &endpoint.token) {
        return Ok(endpoint);
    }

    if let Err(err) = kickstart_agent() {
        return Err(format!(
            "{err}\n\nThe endpoint file at {} names a daemon that is not answering. \
             Run `make install-agent` to reinstall the agent.",
            endpoint_file().display()
        ));
    }

    // No child of ours, so nothing can report an exit code: the health poll is
    // the only signal, exactly as it is for a spawned daemon that never exits.
    let never_exits = Arc::new(Mutex::new(None));
    match wait_until_ready(&endpoint.base_url, &endpoint.token, &never_exits) {
        Ok(()) => Ok(endpoint),
        Err(reason) => Err(format!(
            "{reason}\n\nlaunchd restarted {AGENT_LABEL} but it did not come up. \
             Check ~/Library/Logs/mimir-daemon.log."
        )),
    }
}

/// The development path: no agent installed, so this shell is the parent.
fn spawn_supervised(app: &AppHandle) -> Result<Endpoint, String> {
    let mut last_error = String::new();

    for attempt in 1..=PORT_ATTEMPTS {
        let port = free_port().map_err(|e| format!("could not reserve a local port: {e}"))?;
        let token = mint_token().map_err(|e| format!("could not generate a token: {e}"))?;

        match spawn_once(app, port, &token) {
            Ok(endpoint) => return Ok(endpoint),
            Err(err) => {
                last_error = format!("attempt {attempt}/{PORT_ATTEMPTS}: {err}");
                // Only a bind failure is worth retrying — anything else will
                // fail again the same way on a different port.
                if !err.contains("could not listen") {
                    break;
                }
            }
        }
    }

    Err(last_error)
}

fn spawn_once(app: &AppHandle, port: u16, token: &str) -> Result<Endpoint, String> {
    let mut env = HashMap::new();
    // The entire contract with the Go side. Nothing else is added here: every
    // other value the daemon uses is a constant in internal/config, and adding
    // an env var to this map would create the runtime knob SD-1 says we do not
    // have.
    env.insert("MIMIR_DAEMON_PORT".to_string(), port.to_string());
    env.insert("MIMIR_DAEMON_TOKEN".to_string(), token.to_string());

    let command = app
        .shell()
        .sidecar("mimir-daemon")
        .map_err(|e| {
            format!(
                "the mimir-daemon sidecar binary is missing: {e} — run `make desktop-sidecar` to build it"
            )
        })?
        .envs(env);

    let (mut rx, child) = command
        .spawn()
        .map_err(|e| format!("could not start mimir-daemon: {e}"))?;

    let state = app.state::<DaemonState>();
    *state.child.lock().expect("daemon child poisoned") = Some(child);

    // The child's stderr is its log; its stdout is drained and dropped. Both
    // are consumed so the pipes cannot fill and block the child — and neither
    // is ever parsed for a port or a token.
    let stderr: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let exited = Arc::new(Mutex::new(None::<i32>));
    {
        let stderr = Arc::clone(&stderr);
        let exited = Arc::clone(&exited);
        tauri::async_runtime::spawn(async move {
            while let Some(event) = rx.recv().await {
                match event {
                    CommandEvent::Stderr(line) => {
                        let text = String::from_utf8_lossy(&line).trim_end().to_string();
                        if text.is_empty() {
                            continue;
                        }
                        eprintln!("[mimir-daemon] {text}");
                        let mut buf = stderr.lock().expect("stderr buffer poisoned");
                        if buf.len() == STDERR_KEEP_LINES {
                            buf.remove(0);
                        }
                        buf.push(text);
                    }
                    CommandEvent::Stdout(_) => { /* drained and dropped, never parsed */ }
                    CommandEvent::Terminated(payload) => {
                        *exited.lock().expect("exit status poisoned") =
                            Some(payload.code.unwrap_or(-1));
                        break;
                    }
                    _ => {}
                }
            }
        });
    }

    let base_url = format!("http://127.0.0.1:{port}");
    match wait_until_ready(&base_url, token, &exited) {
        Ok(()) => Ok(Endpoint {
            base_url,
            token: token.to_string(),
        }),
        Err(reason) => {
            if let Some(child) = state.take_child() {
                let _ = child.kill();
            }
            let tail = stderr.lock().expect("stderr buffer poisoned").join("\n");
            if tail.is_empty() {
                Err(reason)
            } else {
                // A bare exit code is not actionable; the daemon's own message
                // ("MIMIR_DAEMON_TOKEN is empty", "could not listen on …") is.
                Err(format!("{reason}\n\n{tail}"))
            }
        }
    }
}

fn wait_until_ready(
    base_url: &str,
    token: &str,
    exited: &Arc<Mutex<Option<i32>>>,
) -> Result<(), String> {
    let deadline = Instant::now() + READY_TIMEOUT;

    loop {
        if let Some(code) = *exited.lock().expect("exit status poisoned") {
            return Err(format!("mimir-daemon exited early with status {code}"));
        }
        if health_ok(base_url, token) {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "mimir-daemon did not answer {base_url}/healthz within {}s",
                READY_TIMEOUT.as_secs()
            ));
        }
        std::thread::sleep(HEALTH_POLL_INTERVAL);
    }
}

fn health_ok(base_url: &str, token: &str) -> bool {
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(2)))
        .build()
        .into();

    match agent
        .get(&format!("{base_url}/healthz"))
        .header("Authorization", &format!("Bearer {token}"))
        .call()
    {
        Ok(mut response) => {
            let mut body = String::new();
            let _ = response
                .body_mut()
                .as_reader()
                .take(4096)
                .read_to_string(&mut body);
            body.contains("\"ok\":true")
        }
        Err(_) => false,
    }
}

// ---------------------------------------------------------------------------
// attached mode: the endpoint file and the launchd job
// ---------------------------------------------------------------------------

/// What `scripts/install-agent.sh` wrote for us: the same port and token it
/// gave launchd, and nothing else. Extra keys are ignored so the file can grow
/// without breaking an older app.
#[derive(Deserialize)]
struct EndpointFile {
    base_url: String,
    token: String,
}

#[derive(Debug)]
enum EndpointError {
    /// No agent is installed. Not an error — it selects the spawn path.
    Missing,
    Unreadable(String),
    Malformed(String),
    /// The file holds a live credential for a process that runs coding
    /// sessions with file tools. Group- or world-readable is a refusal, not a
    /// warning: reading it anyway would launder a real exposure into a
    /// working app.
    Permissive(u32),
}

impl std::fmt::Display for EndpointError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let path = endpoint_file();
        match self {
            Self::Missing => write!(f, "no daemon endpoint at {}", path.display()),
            Self::Unreadable(err) => {
                write!(f, "could not read {}: {err}", path.display())
            }
            Self::Malformed(what) => write!(
                f,
                "{} is not a usable endpoint file ({what}) — run `make install-agent` to rewrite it",
                path.display()
            ),
            Self::Permissive(mode) => write!(
                f,
                "refusing to read {}: it is mode {mode:04o} and holds the daemon token; \
                 run `chmod 600` on it, or `make install-agent` to rewrite it",
                path.display()
            ),
        }
    }
}

/// Where the installer puts the endpoint file: alongside the store, under
/// `os.UserConfigDir()` as Go computes it on macOS.
fn endpoint_file() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_default();
    PathBuf::from(home)
        .join("Library/Application Support/mimir")
        .join("endpoint.json")
}

fn read_endpoint(path: &std::path::Path) -> Result<Endpoint, EndpointError> {
    let meta = match fs::metadata(path) {
        Ok(meta) => meta,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
            return Err(EndpointError::Missing)
        }
        Err(err) => return Err(EndpointError::Unreadable(err.to_string())),
    };

    let mode = meta.permissions().mode() & 0o777;
    if mode & 0o077 != 0 {
        return Err(EndpointError::Permissive(mode));
    }

    let raw = fs::read_to_string(path).map_err(|e| EndpointError::Unreadable(e.to_string()))?;
    let parsed: EndpointFile =
        serde_json::from_str(&raw).map_err(|e| EndpointError::Malformed(e.to_string()))?;

    // The same guard `daemon_request` applies to a path, applied to the base:
    // an endpoint file that named another host would send the token there.
    if !parsed.base_url.starts_with("http://127.0.0.1:") {
        return Err(EndpointError::Malformed(format!(
            "base_url {:?} is not loopback",
            parsed.base_url
        )));
    }
    if parsed.token.len() < 32 || !parsed.token.chars().all(|c| c.is_ascii_hexdigit()) {
        return Err(EndpointError::Malformed(
            "token is not 32+ hex characters".to_string(),
        ));
    }

    Ok(Endpoint {
        base_url: parsed.base_url.trim_end_matches('/').to_string(),
        token: parsed.token,
    })
}

/// Asks launchd to restart the job it owns. `-k` kills the current instance
/// first, so this is a restart and not a no-op against a wedged process.
fn kickstart_agent() -> Result<(), String> {
    let target = format!("gui/{}/{AGENT_LABEL}", current_uid());
    let output = std::process::Command::new("/bin/launchctl")
        .args(["kickstart", "-k", &target])
        .output()
        .map_err(|e| format!("could not run launchctl: {e}"))?;

    if output.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    Err(format!(
        "launchctl could not restart {target}: {}",
        if stderr.is_empty() {
            format!("exit status {}", output.status)
        } else {
            stderr
        }
    ))
}

fn current_uid() -> u32 {
    // Safe: getuid() takes no arguments, touches no memory, and cannot fail.
    unsafe { libc_getuid() }
}

#[cfg(unix)]
extern "C" {
    #[link_name = "getuid"]
    fn libc_getuid() -> u32;
}

/// Reserves a port by binding it, reading the number the kernel assigned, and
/// letting go.
///
/// The alternative — passing 0 and letting the daemon bind an ephemeral port —
/// would require reading the resolved address back out of the child's output,
/// which is exactly the design this file exists to avoid.
fn free_port() -> std::io::Result<u16> {
    let listener = TcpListener::bind("127.0.0.1:0")?;
    let port = listener.local_addr()?.port();
    drop(listener);
    Ok(port)
}

/// 32 bytes from the OS CSPRNG, hex-encoded, fresh per launch.
///
/// Passed in the environment rather than on the command line: argv is readable
/// by any process on the machine (`ps`), and this token starts coding sessions
/// with file tools.
fn mint_token() -> Result<String, getrandom::Error> {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes)?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}

/// Signals the child on app exit and gives it a moment to drain.
///
/// Only ever a child *we* spawned. In attached mode there is none — the daemon
/// belongs to launchd and is meant to outlive this window, which is the whole
/// point of installing the agent — so `take_child()` returns `None` and this is
/// a no-op. For a spawned one the graceful path matters: the daemon shuts the
/// HTTP server down on SIGTERM and waits for in-flight coding runs, and
/// skipping it would orphan a process holding the store's write lock.
pub fn shutdown(app: &AppHandle) {
    let state = app.state::<DaemonState>();
    if let Some(child) = state.take_child() {
        let pid = child.pid();
        // The shell plugin's kill() is a hard kill, so send SIGTERM first and
        // only fall back to it.
        #[cfg(unix)]
        unsafe {
            libc_kill(pid as i32, 15);
        }
        std::thread::sleep(Duration::from_millis(400));
        let _ = child.kill();
    }
}

#[cfg(unix)]
extern "C" {
    #[link_name = "kill"]
    fn libc_kill(pid: i32, sig: i32) -> i32;
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The proxy must stay a daemon proxy. Each of these would turn it into an
    /// open one that attaches a live bearer token to the request.
    #[test]
    fn proxy_rejects_anything_that_is_not_a_daemon_path() {
        for path in [
            "http://evil.example/steal",
            "//evil.example/steal",
            "https://169.254.169.254/latest/meta-data/",
            "healthz",
            "",
        ] {
            assert!(
                !(path.starts_with('/') && !path.starts_with("//") && !path.contains("://")),
                "{path:?} would have been proxied"
            );
        }
        for path in ["/healthz", "/projects", "/coding-tasks/run-1"] {
            assert!(path.starts_with('/') && !path.starts_with("//") && !path.contains("://"));
        }
    }

    #[test]
    fn token_is_fresh_and_hex() {
        let a = mint_token().expect("token");
        let b = mint_token().expect("token");

        assert_eq!(a.len(), 64, "32 bytes hex-encoded");
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b, "a token must be per-launch, not per-install");
    }

    #[test]
    fn reserved_port_is_actually_free() {
        let port = free_port().expect("port");
        assert!(port > 0);
        // The listener must really have been released, or the child could
        // never bind what we just handed it.
        TcpListener::bind(("127.0.0.1", port)).expect("port should be bindable after reservation");
    }

    /// The endpoint file is the whole attached-mode contract, and every one of
    /// these rejections is a real failure mode: a hand-edited file, a token
    /// left world-readable, an installer pointed somewhere else.
    #[test]
    fn endpoint_file_is_read_only_when_it_is_well_formed_and_private() {
        let dir = std::env::temp_dir().join(format!("mimir-endpoint-{}", std::process::id()));
        std::fs::create_dir_all(&dir).expect("temp dir");
        let token = "a".repeat(64);

        let write = |name: &str, body: &str, mode: u32| {
            let path = dir.join(name);
            std::fs::write(&path, body).expect("write");
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(mode)).expect("chmod");
            path
        };

        let good = write(
            "good.json",
            &format!(r#"{{"base_url":"http://127.0.0.1:41999/","port":41999,"token":"{token}"}}"#),
            0o600,
        );
        let endpoint = read_endpoint(&good).expect("a private, well-formed file must be read");
        assert_eq!(endpoint.base_url, "http://127.0.0.1:41999");
        assert_eq!(endpoint.token, token);

        let missing = dir.join("absent.json");
        assert!(
            matches!(read_endpoint(&missing), Err(EndpointError::Missing)),
            "an absent file selects the spawn path, it is not an error"
        );

        let loose = write(
            "loose.json",
            &format!(r#"{{"base_url":"http://127.0.0.1:41999","token":"{token}"}}"#),
            0o644,
        );
        assert!(
            matches!(read_endpoint(&loose), Err(EndpointError::Permissive(0o644))),
            "a world-readable token file must be refused, not read"
        );

        let remote = write(
            "remote.json",
            &format!(r#"{{"base_url":"http://evil.example:80","token":"{token}"}}"#),
            0o600,
        );
        assert!(
            matches!(read_endpoint(&remote), Err(EndpointError::Malformed(_))),
            "a non-loopback base_url would send the token off the machine"
        );

        let weak = write(
            "weak.json",
            r#"{"base_url":"http://127.0.0.1:41999","token":"short"}"#,
            0o600,
        );
        assert!(matches!(
            read_endpoint(&weak),
            Err(EndpointError::Malformed(_))
        ));

        let junk = write("junk.json", "not json at all", 0o600);
        assert!(matches!(
            read_endpoint(&junk),
            Err(EndpointError::Malformed(_))
        ));

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn endpoint_path_sits_next_to_the_store() {
        let path = endpoint_file();
        assert!(path.ends_with("Library/Application Support/mimir/endpoint.json"));
    }

    #[test]
    fn health_is_false_when_nothing_listens() {
        // Port 1 on loopback: nothing listens, and the poll must report that
        // rather than hang or panic.
        assert!(!health_ok("http://127.0.0.1:1", "token"));
    }

    #[test]
    fn a_pipeline_route_gets_the_long_budget() {
        // A lead-gen run scrapes, classifies and synthesizes in one request.
        // Thirty seconds for that is what produced "timeout: global" against a
        // daemon that was still working.
        assert_eq!(request_timeout("/maps/leadgen"), PIPELINE_REQUEST_TIMEOUT);
        assert_eq!(
            request_timeout("/maps/leadgen/export"),
            PIPELINE_REQUEST_TIMEOUT
        );
        assert_eq!(request_timeout("/brain/scan/now"), PIPELINE_REQUEST_TIMEOUT);
    }

    #[test]
    fn everything_else_gets_the_default_budget() {
        assert_eq!(request_timeout("/healthz"), DEFAULT_REQUEST_TIMEOUT);
        assert_eq!(request_timeout("/maps/leads"), DEFAULT_REQUEST_TIMEOUT);
        // A prefix match must be on a path segment, not on the string: the
        // ledger reads are not the run route with a longer name.
        assert_eq!(
            request_timeout("/maps/leadgenerator"),
            DEFAULT_REQUEST_TIMEOUT
        );
    }

    #[test]
    fn a_query_string_does_not_hide_the_route() {
        assert_eq!(
            request_timeout("/brain/scan/now?force=1"),
            PIPELINE_REQUEST_TIMEOUT
        );
        assert_eq!(
            request_timeout("/maps/leads?limit=50"),
            DEFAULT_REQUEST_TIMEOUT
        );
    }

    #[test]
    fn ready_gives_up_when_the_child_exited() {
        let exited = Arc::new(Mutex::new(Some(1)));
        let err = wait_until_ready("http://127.0.0.1:1", "token", &exited)
            .expect_err("an exited child cannot become ready");
        assert!(err.contains("exited early"), "unhelpful message: {err}");
    }
}
