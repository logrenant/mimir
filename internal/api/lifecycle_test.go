package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/store"
)

// start:false is the board writing intent down; without it the route keeps its
// original behaviour, which is what an older client depends on.
func TestStartCodingTask_StartFalseCreatesWithoutRunning(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1", Status: "backlog"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken,
		`{"project_id":"p","prompt":"later","title":"a card","start":false}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", w.Code)
	}
	if !runner.created {
		t.Error("start:false must not spawn a run")
	}
	if runner.lastCreate.Title != "a card" {
		t.Errorf("title was dropped: %+v", runner.lastCreate)
	}
}

func TestStartCodingTask_DefaultsToRunning(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1", Status: "queued"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken,
		`{"project_id":"p","prompt":"now"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", w.Code)
	}
	if runner.created {
		t.Error("without start:false the task must be released")
	}
}

func TestStartCodingTask_CarriesAttachmentIDs(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken,
		`{"project_id":"p","prompt":"look","attachment_ids":["a1","a2"]}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", w.Code)
	}
	if len(runner.lastCreate.AttachmentIDs) != 2 {
		t.Errorf("attachment ids were dropped: %+v", runner.lastCreate)
	}
}

func TestEnqueueAndStopReachTheRunner(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	if w := do(h, http.MethodPost, "/coding-tasks/r1/enqueue", testToken, ""); w.Code != http.StatusAccepted {
		t.Fatalf("enqueue status: got %d, want 202", w.Code)
	}
	if runner.enqueued != "r1" {
		t.Errorf("enqueued: got %q", runner.enqueued)
	}

	if w := do(h, http.MethodPost, "/coding-tasks/r1/stop", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("stop status: got %d, want 200", w.Code)
	}
	if runner.stopped != "r1" {
		t.Errorf("stopped: got %q", runner.stopped)
	}

	if w := do(h, http.MethodDelete, "/coding-tasks/r1", testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete status: got %d, want 204", w.Code)
	}
	if runner.deleted != "r1" {
		t.Errorf("deleted: got %q", runner.deleted)
	}
}

// The card the operator clicked was a moment out of date — that is a conflict,
// not a bad request and not our failure.
func TestStopCodingTask_NotStoppableIs409(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("%w: it is completed", coderunner.ErrNotStoppable)}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks/r1/stop", testToken, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", w.Code)
	}
	if env := decodeError(t, w); env.Error.Code != codeConflict {
		t.Errorf("code: got %q, want %q", env.Error.Code, codeConflict)
	}
}

func TestAttachment_UploadAndFetchRoundTrip(t *testing.T) {
	runner := &fakeRunner{attachment: coderunner.Attachment{ID: "att-1", MIME: "image/png"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	raw := []byte{0x89, 'P', 'N', 'G'}
	body := fmt.Sprintf(`{"filename":"shot.png","data_base64":%q}`,
		base64.StdEncoding.EncodeToString(raw))

	w := do(h, http.MethodPost, "/coding-tasks/attachments", testToken, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status: got %d, want 201 (%s)", w.Code, w.Body.String())
	}
	if string(runner.attachmentRaw) != string(raw) {
		t.Errorf("the bytes did not reach the runner: %q", runner.attachmentRaw)
	}

	w = do(h, http.MethodGet, "/coding-tasks/attachments/att-1", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("fetch status: got %d, want 200", w.Code)
	}
	var got attachmentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	// base64 rather than a URL: the desktop CSP allows data: images and does
	// not allow http://127.0.0.1 ones.
	if got.DataBase64 != base64.StdEncoding.EncodeToString(raw) {
		t.Errorf("data_base64: got %q", got.DataBase64)
	}
}

func TestAttachment_RejectsBadBase64(t *testing.T) {
	runner := &fakeRunner{}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks/attachments", testToken,
		`{"filename":"x.png","data_base64":"not base64!!"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if runner.calls != 0 {
		t.Error("the runner was asked to store something undecodable")
	}
}

func TestAttachment_NonImageIs400(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("%w: text/plain", coderunner.ErrAttachmentType)}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks/attachments", testToken,
		`{"filename":"x.txt","data_base64":"aGVsbG8="}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// An image is the one payload here that is not text, and its ceiling is its
// own constant rather than a relaxation of everyone else's.
func TestAttachment_HasItsOwnBodyCeiling(t *testing.T) {
	cfg := testConfig()
	cfg.DaemonMaxRequestBytes = 32
	cfg.CodingAttachmentMaxBytes = 1 << 20

	runner := &fakeRunner{attachment: coderunner.Attachment{ID: "att-1"}}
	h := New(cfg, Deps{Runner: runner}).Handler()

	big := make([]byte, 4096)
	body := fmt.Sprintf(`{"filename":"shot.png","data_base64":%q}`,
		base64.StdEncoding.EncodeToString(big))

	if w := do(h, http.MethodPost, "/coding-tasks/attachments", testToken, body); w.Code != http.StatusCreated {
		t.Fatalf("upload status: got %d, want 201 (%s)", w.Code, w.Body.String())
	}
	// Every other route keeps the small cap.
	if w := do(h, http.MethodPost, "/coding-tasks", testToken, body); w.Code != http.StatusBadRequest {
		t.Fatalf("a normal route must still be capped: got %d", w.Code)
	}
}

// A nil Runner used to mean a nil-interface call inside the handler, which
// recoverPanics turned into a 500 for a route that does not exist here.
func TestCodingTaskRoutesAreAbsentWithoutARunner(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()

	for _, path := range []string{"/coding-tasks", "/coding-tasks/r1", "/coding-tasks/attachments/a1", "/coding-models"} {
		if w := do(h, http.MethodGet, path, testToken, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, w.Code)
		}
	}
}

// ---- accounts --------------------------------------------------------------

type fakeAccounts struct {
	list  []account.Account
	login account.LoginState
	// started, code and reset record what the handler asked of the registry.
	started bool
	code    string
	reset   bool
	err     error
}

func (f *fakeAccounts) List(context.Context) ([]account.Account, error) {
	return f.list, f.err
}

func (f *fakeAccounts) Get(_ context.Context, id string) (account.Account, error) {
	for _, a := range f.list {
		if a.ID == id {
			return a, nil
		}
	}
	return account.Account{}, fmt.Errorf("%w: %s", account.ErrAccountNotFound, id)
}

func (f *fakeAccounts) StartLogin(context.Context) (account.LoginState, error) {
	f.started = true
	return f.login, f.err
}

func (f *fakeAccounts) LoginState() account.LoginState { return f.login }

func (f *fakeAccounts) SubmitCode(code string) error {
	f.code = code
	return f.err
}

func (f *fakeAccounts) Reset(context.Context) error {
	f.reset = true
	return f.err
}

// Connecting is a login the daemon runs, not a directory a client names: the
// route takes no body and there is no path on the wire at all.
func TestStartLogin_TakesNoInputAndReportsTheWindow(t *testing.T) {
	accounts := &fakeAccounts{login: account.LoginState{
		State:   account.LoginWaiting,
		URL:     "https://claude.com/cai/oauth/authorize?code=true",
		Message: "Giriş sayfası Chrome'da gizli pencerede açıldı.",
	}}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts/login", testToken, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202 (%s)", w.Code, w.Body.String())
	}
	if !accounts.started {
		t.Error("the login was never started")
	}
	// The URL is echoed even though the daemon opened it: a window that landed
	// behind another app leaves the operator with a link to use.
	if !strings.Contains(w.Body.String(), "oauth/authorize") {
		t.Errorf("the authorization URL did not reach the client: %s", w.Body.String())
	}
}

// The registration route is gone rather than deprecated: there is no directory
// to accept, and a POST that quietly did nothing would be worse than a refusal.
func TestRegisterAccount_RouteIsGone(t *testing.T) {
	accounts := &fakeAccounts{}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts", testToken,
		`{"label":"ikinci hesap","config_dir":"/Users/x/.claude-accounts/b"}`)
	if w.Code < 400 {
		t.Errorf("status: got %d, want a refusal (%s)", w.Code, w.Body.String())
	}
}

// Same for the scan: the accounts directory is no longer an authority for
// anything, because Mimir signs into a slot of its own.
func TestScanAccounts_RouteIsGone(t *testing.T) {
	accounts := &fakeAccounts{}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts/scan", testToken, "")
	if w.Code < 400 {
		t.Errorf("status: got %d, want a refusal (%s)", w.Code, w.Body.String())
	}
}

// The reset is both the "çıkış yap" button and what the desktop shell calls on
// its way out, so it takes no arguments — at that point there is no id to hold.
func TestResetAccounts_SignsOut(t *testing.T) {
	accounts := &fakeAccounts{}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts/reset", testToken, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204 (%s)", w.Code, w.Body.String())
	}
	if !accounts.reset {
		t.Error("the registry was never asked to reset")
	}
}

// The paste-a-code fallback: the code is typed straight into the waiting
// process, and an empty one is the client's mistake rather than the daemon's.
func TestLoginCode_ReachesTheWaitingLogin(t *testing.T) {
	accounts := &fakeAccounts{login: account.LoginState{State: account.LoginCode}}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts/login/code", testToken, `{"code":"abc123"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204 (%s)", w.Code, w.Body.String())
	}
	if accounts.code != "abc123" {
		t.Errorf("the code did not reach the registry: %q", accounts.code)
	}
}

// The daemon's own model calls no longer take a slot from anywhere, so the
// route that used to point them at one is gone rather than deprecated.
//
// Asserted as "not a success" rather than as one exact status: with the route
// unregistered the path now falls to another pattern, which answers 404 or 405.
// Which of the two the mux picks is its business — what matters here is that
// nothing accepts the request and quietly does nothing.
func TestSetBackgroundAccount_RouteIsGone(t *testing.T) {
	accounts := &fakeAccounts{list: []account.Account{{ID: "a1", Label: "Claude"}}}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts/background", testToken, `{"account_id":"a2"}`)
	if w.Code < 400 {
		t.Errorf("status: got %d, want a refusal (%s)", w.Code, w.Body.String())
	}
}

func TestStartCodingTask_PinsTheAccount(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken,
		`{"project_id":"p","prompt":"go","account_id":"a1"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202 (%s)", w.Code, w.Body.String())
	}
	if runner.lastCreate.AccountID != "a1" {
		t.Errorf("the pin was dropped: %+v", runner.lastCreate)
	}
}

// Absent is what every client sends now: there is one account, so which
// identity pays is not a choice a composer offers. The field stays on the wire
// for clients written before that, which is what the test above covers.
func TestStartCodingTask_NoAccountIsTheNormalCase(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken, `{"project_id":"p","prompt":"go"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", w.Code)
	}
	if runner.lastCreate.AccountID != "" {
		t.Errorf("AccountID should be empty, got %q", runner.lastCreate.AccountID)
	}
}

func TestAccountRoutesAreAbsentWithoutARegistry(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()

	if w := do(h, http.MethodGet, "/accounts", testToken, ""); w.Code != http.StatusNotFound {
		t.Errorf("/accounts: got %d, want 404", w.Code)
	}
}

// ---- models ----------------------------------------------------------------

// The picker is a view of the daemon's constant, so the route has to say which
// entry is the default — a form that cannot preselect it would make every task
// look explicitly pinned.
func TestListCodingModels_OffersTheAllowListWithADefault(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, Deps{Runner: &fakeRunner{}}).Handler()

	w := do(h, http.MethodGet, "/coding-models", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}

	var body struct {
		Models []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Default bool   `json:"default"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Models) != len(cfg.CodingModels) {
		t.Fatalf("got %d models, want %d", len(body.Models), len(cfg.CodingModels))
	}

	defaults := 0
	for _, m := range body.Models {
		if m.Label == "" {
			t.Errorf("model %q has no label", m.ID)
		}
		if m.Default {
			defaults++
			if m.ID != cfg.CodingModel {
				t.Errorf("default is %q, want %q", m.ID, cfg.CodingModel)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("got %d defaults, want exactly 1", defaults)
	}
}

func TestStartCodingTask_CarriesTheModel(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken,
		`{"project_id":"p","prompt":"think","model":"claude-opus-5"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", w.Code)
	}
	if runner.lastCreate.Model != "claude-opus-5" {
		t.Errorf("model was dropped: %+v", runner.lastCreate)
	}
}

// An unknown model is the operator's mistake in the form they are still
// looking at, so it is a 400 rather than a run that dies a few seconds later.
func TestStartCodingTask_UnknownModelIs400(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("%w: gpt-9", coderunner.ErrUnknownModel)}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks", testToken,
		`{"project_id":"p","prompt":"x","model":"gpt-9"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// The two buttons on a failed card are one route and one flag, so the flag is
// the only thing worth asserting about the wire.
func TestRetryCodingTask_CarriesTheFreshFlag(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		// No body at all is what "devam et" sends, and continuing is the point
		// of retrying a run that already did half the work.
		{"no body continues", "", false},
		{"explicit continue", `{"fresh":false}`, false},
		{"start over", `{"fresh":true}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{run: coderunner.Run{ID: "r1", Status: "queued"}}
			h := New(testConfig(), Deps{Runner: runner}).Handler()

			w := do(h, http.MethodPost, "/coding-tasks/r1/retry", testToken, tc.body)
			if w.Code != http.StatusAccepted {
				t.Fatalf("status: got %d, want 202 (%s)", w.Code, w.Body.String())
			}
			if runner.retried != "r1" {
				t.Errorf("retried: got %q, want r1", runner.retried)
			}
			if runner.retriedFresh != tc.want {
				t.Errorf("fresh: got %v, want %v", runner.retriedFresh, tc.want)
			}
		})
	}
}

// A patch carries only the fields the form owns, and an absent one must reach
// the runner as "not touched" rather than as an empty string.
func TestEditCodingTask_PatchesOnlyWhatItSends(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1", Status: "backlog"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPatch, "/coding-tasks/r1", testToken,
		`{"title":"yeni ad","attachment_ids":["abc123"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if runner.edited != "r1" {
		t.Errorf("edited: got %q, want r1", runner.edited)
	}
	if runner.lastEdit.Title == nil || *runner.lastEdit.Title != "yeni ad" {
		t.Errorf("title: got %v, want \"yeni ad\"", runner.lastEdit.Title)
	}
	if runner.lastEdit.Prompt != nil {
		t.Errorf("an untouched prompt must stay untouched, got %q", *runner.lastEdit.Prompt)
	}
	if runner.lastEdit.AttachmentIDs == nil || len(*runner.lastEdit.AttachmentIDs) != 1 {
		t.Errorf("attachment_ids: got %v", runner.lastEdit.AttachmentIDs)
	}
}

// Clearing an image list is a real edit; clearing the prompt is not, because a
// card with no prompt asks for nothing.
func TestEditCodingTask_RefusesAnEmptiedPromptAndAcceptsAnEmptiedGallery(t *testing.T) {
	runner := &fakeRunner{run: coderunner.Run{ID: "r1", Status: "backlog"}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	if w := do(h, http.MethodPatch, "/coding-tasks/r1", testToken, `{"prompt":"   "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if runner.edited != "" {
		t.Error("a refused edit must not reach the runner")
	}

	w := do(h, http.MethodPatch, "/coding-tasks/r1", testToken, `{"attachment_ids":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if runner.lastEdit.AttachmentIDs == nil || len(*runner.lastEdit.AttachmentIDs) != 0 {
		t.Errorf("an emptied gallery must reach the runner as empty, got %v", runner.lastEdit.AttachmentIDs)
	}
}

// A card the operator was editing while it started is a conflict, like every
// other too-late click on this board.
func TestEditCodingTask_NotEditableIsAConflict(t *testing.T) {
	runner := &fakeRunner{err: coderunner.ErrNotEditable}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	if w := do(h, http.MethodPatch, "/coding-tasks/r1", testToken, `{"title":"x"}`); w.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", w.Code)
	}
}

// The board's answer to a card that has sat in Queued: ask the dispatcher
// again, and be told why when the answer is "nothing can start".
func TestKickQueue_PumpsAndReportsWhyItCannot(t *testing.T) {
	runner := &fakeRunner{}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	if w := do(h, http.MethodPost, "/coding-tasks/queue/kick", testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", w.Code)
	}
	if runner.kicked != 1 {
		t.Errorf("kicked: got %d, want 1", runner.kicked)
	}

	stalled := &fakeRunner{kickErr: account.ErrNotConnected}
	h = New(testConfig(), Deps{Runner: stalled}).Handler()
	w := do(h, http.MethodPost, "/coding-tasks/queue/kick", testToken, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "connect") {
		t.Errorf("the answer must say what to do: %s", w.Body.String())
	}
}

// A spent token budget answers the kick the same way a missing login does —
// 409 with the reason — because the operator's question is the same one, and
// the only useful part of the answer is when the queue moves again.
func TestKickQueue_SpentBudgetIsAConflict(t *testing.T) {
	runner := &fakeRunner{kickErr: fmt.Errorf("%w — the queue restarts by itself at 19:40",
		coderunner.ErrBudgetSpent)}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodPost, "/coding-tasks/queue/kick", testToken, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "19:40") {
		t.Errorf("the answer must carry the time it restarts: %s", w.Body.String())
	}
}

// The log is only worth keeping if it can be read back. Holds are the live
// state and the log is durable, and the route serves both.
func TestQueueLimits_ServesHoldsAndTheLog(t *testing.T) {
	resets := time.Date(2026, 9, 5, 19, 40, 0, 0, time.UTC)
	runner := &fakeRunner{limits: coderunner.LimitReport{
		Holds: []coderunner.Hold{{AccountID: "acct", ResetsAt: resets, Reason: "usage limit"}},
		Log: []store.RateLimitRow{
			{ID: 1, Phase: store.RateLimitPhaseRun, AccountID: "acct", RunID: "r1", ResetsAt: resets},
		},
	}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	w := do(h, http.MethodGet, "/coding-tasks/queue/limits", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var got coderunner.LimitReport
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a LimitReport: %v (%s)", err, w.Body.String())
	}
	if len(got.Holds) != 1 || !got.Holds[0].ResetsAt.Equal(resets) {
		t.Errorf("holds not served: %+v", got.Holds)
	}
	if len(got.Log) != 1 || got.Log[0].Phase != store.RateLimitPhaseRun {
		t.Errorf("log not served: %+v", got.Log)
	}
}

// A card the operator clicked a moment too late is a conflict, not a failure:
// 409 blames the state, which is the thing that has to change.
func TestRetryCodingTask_NotRetryableIsAConflict(t *testing.T) {
	runner := &fakeRunner{err: coderunner.ErrNotRetryable}
	h := New(testConfig(), Deps{Runner: runner}).Handler()

	if w := do(h, http.MethodPost, "/coding-tasks/r1/retry", testToken, `{"fresh":false}`); w.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", w.Code)
	}
}
