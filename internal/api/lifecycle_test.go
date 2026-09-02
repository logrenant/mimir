package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/coderunner"
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
	list       []account.Account
	registered account.Account
	lastLabel  string
	lastDir    string
	deleted    string
	err        error
}

func (f *fakeAccounts) Register(_ context.Context, label, configDir string) (account.Account, error) {
	f.lastLabel = label
	f.lastDir = configDir
	return f.registered, f.err
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

func (f *fakeAccounts) Delete(_ context.Context, id string) error {
	f.deleted = id
	return f.err
}

// config_dir is the second and last route that accepts a filesystem path, and
// it is validated once here exactly as POST /projects is.
func TestRegisterAccount_TakesTheConfigDirectoryOnce(t *testing.T) {
	accounts := &fakeAccounts{registered: account.Account{ID: "a1", Label: "b"}}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts", testToken,
		`{"label":"ikinci hesap","config_dir":"/Users/x/.claude-accounts/b"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (%s)", w.Code, w.Body.String())
	}
	if accounts.lastDir != "/Users/x/.claude-accounts/b" || accounts.lastLabel != "ikinci hesap" {
		t.Errorf("the registry was called with %q / %q", accounts.lastLabel, accounts.lastDir)
	}
}

func TestRegisterAccount_BadDirectoryIs400(t *testing.T) {
	accounts := &fakeAccounts{err: fmt.Errorf("%w: relative", account.ErrInvalidDir)}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodPost, "/accounts", testToken, `{"label":"x","config_dir":"slot-b"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// Forgetting a slot with a queue behind it would strand work nothing can drain.
func TestDeleteAccount_InUseIs409(t *testing.T) {
	accounts := &fakeAccounts{err: fmt.Errorf("%w: 2 run(s)", account.ErrAccountInUse)}
	h := New(testConfig(), Deps{Accounts: accounts}).Handler()

	w := do(h, http.MethodDelete, "/accounts/a1", testToken, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", w.Code)
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

// Absent means "any free one", which is the default because it is what makes a
// second account worth registering.
func TestStartCodingTask_NoAccountMeansAutomatic(t *testing.T) {
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
