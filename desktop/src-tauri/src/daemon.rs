//! Supervises the `goat-daemon` sidecar.
//!
//! The parent owns the plumbing, and that is the whole design. goat v1 had its
//! shell scrape `GOAT_PORT=<n>` out of the child's stdout; this repo made that
//! impossible on the child's side (SD-4 — stdout carries MCP frames and nothing
//! else, and the daemon logs its resolved address to stderr precisely because
//! the parent is supposed to already know it). So here: **we** pick the port,
//! **we** mint the token, we tell the child, and we tell the WebView. Nothing
//! in this file reads either value back out of a pipe.

use std::collections::HashMap;
use std::io::Read;
use std::net::TcpListener;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde::Serialize;
use tauri::{AppHandle, Manager, State};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

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
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(30)))
        // A 4xx is an answer, not a transport failure: the daemon's error
        // envelope is the useful part and must reach the caller intact.
        .http_status_as_error(false)
        .build()
        .into();

    let url = format!("{}{}", endpoint.base_url, path);
    let auth = format!("Bearer {}", endpoint.token);

    // GET and POST only, because those are the only verbs the daemon's routes
    // answer to. An allowlist here rather than a pass-through keeps this from
    // becoming a general HTTP client the WebView can steer.
    let result = match (method.to_ascii_uppercase().as_str(), body) {
        ("GET", _) => agent.get(&url).header("Authorization", &auth).call(),
        ("POST", Some(payload)) => agent
            .post(&url)
            .header("Authorization", &auth)
            .header("Content-Type", "application/json")
            .send(payload),
        ("POST", None) => agent.post(&url).header("Authorization", &auth).send_empty(),
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
        Err(err) => Err(format!("could not reach the daemon: {err}")),
    }
}

/// Retry after a failed start, so a transient cause (a port that was briefly
/// taken, a sidecar built a second too late) does not need an app restart.
#[tauri::command]
pub fn restart_daemon(app: AppHandle) {
    let state = app.state::<DaemonState>();
    if let Some(child) = state.take_child() {
        let _ = child.kill();
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
    env.insert("GOAT_DAEMON_PORT".to_string(), port.to_string());
    env.insert("GOAT_DAEMON_TOKEN".to_string(), token.to_string());

    let command = app
        .shell()
        .sidecar("goat-daemon")
        .map_err(|e| {
            format!(
                "the goat-daemon sidecar binary is missing: {e} — run `make desktop-sidecar` to build it"
            )
        })?
        .envs(env);

    let (mut rx, child) = command
        .spawn()
        .map_err(|e| format!("could not start goat-daemon: {e}"))?;

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
                        eprintln!("[goat-daemon] {text}");
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
                // ("GOAT_DAEMON_TOKEN is empty", "could not listen on …") is.
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
            return Err(format!("goat-daemon exited early with status {code}"));
        }
        if health_ok(base_url, token) {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "goat-daemon did not answer {base_url}/healthz within {}s",
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
/// The daemon has a graceful path — it shuts the HTTP server down on SIGTERM
/// and waits for in-flight coding runs — and skipping it would orphan a process
/// holding the store's write lock.
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

    #[test]
    fn health_is_false_when_nothing_listens() {
        // Port 1 on loopback: nothing listens, and the poll must report that
        // rather than hang or panic.
        assert!(!health_ok("http://127.0.0.1:1", "token"));
    }

    #[test]
    fn ready_gives_up_when_the_child_exited() {
        let exited = Arc::new(Mutex::new(Some(1)));
        let err = wait_until_ready("http://127.0.0.1:1", "token", &exited)
            .expect_err("an exited child cannot become ready");
        assert!(err.contains("exited early"), "unhelpful message: {err}");
    }
}
