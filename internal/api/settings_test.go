package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/connections"
	"github.com/logrenant/mimir/internal/settings"
)

func settingsHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(testConfig(), Deps{Settings: settings.New(t.TempDir())}).Handler()
}

// The class defaults are the most dangerous values on this surface: a per-run
// selection is spent once, with somebody watching; these are spent by every
// Brain pass and every refine afterwards, with nobody re-reading them. They go
// through the same allow-list, and this is that.
func TestSaveSettings_RefusesAClassDefaultOutsideTheTable(t *testing.T) {
	h := settingsHandler(t)

	w := do(h, http.MethodPut, "/settings", testToken,
		`{"provider":"","model":"","distill":{"provider":"uydurma","model":"x"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d: %s", w.Code, w.Body.String())
	}
}

func TestSaveSettings_StoresAndReturnsTheClassDefaults(t *testing.T) {
	h := settingsHandler(t)

	w := do(h, http.MethodPut, "/settings", testToken,
		`{"provider":"","model":"","distill":{"provider":"gemini","model":"gemini-2.5-flash"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d: %s", w.Code, w.Body.String())
	}

	var got struct {
		Distill struct{ Provider, Model string } `json:"distill"`
		Reason  struct{ Provider, Model string } `json:"reason"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Distill.Provider != "gemini" || got.Distill.Model != "gemini-2.5-flash" {
		t.Errorf("distil varsayılanı saklanmadı: %+v", got.Distill)
	}
	// An omitted class stays unset rather than inheriting the other one.
	if got.Reason.Provider != "" {
		t.Errorf("gönderilmeyen sınıf dolduruldu: %+v", got.Reason)
	}
}

// The connection-shaped view of the registry, and the two things it must never
// carry: a secret, or anything that names what runs.
func TestListConnections_ReportsWhatEachConnectionIs(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, Deps{Connections: connections.New(cfg)}).Handler()

	w := do(h, http.MethodGet, "/llm/connections", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d: %s", w.Code, w.Body.String())
	}

	var got struct {
		Connections []struct {
			ID, Label, Vendor, Adapter, Transport string
			Builtin                               bool
		} `json:"connections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Connections) != 4 {
		t.Fatalf("beklenen 4 bağlantı, alınan %d", len(got.Connections))
	}

	byID := map[string]string{}
	for _, c := range got.Connections {
		byID[c.ID] = c.Vendor
		if c.Transport != "cli" {
			t.Errorf("%s taşıması %q", c.ID, c.Transport)
		}
		if !c.Builtin {
			t.Errorf("%s yerleşik işaretlenmemiş", c.ID)
		}
	}
	// Antigravity has no subscription of its own — it rides a Google AI plan —
	// so both Google rows say Google.
	if byID["agy"] != "google" || byID["gemini"] != "google" {
		t.Errorf("satıcılar: %v", byID)
	}

	// Nothing on this surface may name a binary, a flag or a path.
	for _, forbidden := range []string{"cli_path", "/usr/", "--", "api_key", "secret"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Errorf("bağlantı yüzeyi %q taşıyor: %s", forbidden, w.Body.String())
		}
	}
}

// A daemon built without a registry answers with an empty list rather than a
// 500 — the same degradation the availability field already has.
func TestListConnections_IsEmptyWithoutARegistry(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()
	w := do(h, http.MethodGet, "/llm/connections", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"connections":[]`) {
		t.Errorf("boş liste bekleniyordu: %s", w.Body.String())
	}
}

// The extension point, working by refusing.
//
// Every provider an operator could add today is an API one and none of those
// adapters is written — an adapter built from documentation and never exercised
// fails at the first real call, or half-works and returns prose where a schema
// was asked for. So the route exists in its final shape and answers with the
// provider's name and the reason.
func TestAddConnection_RefusesAProviderThisBuildCannotRunYet(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, Deps{Connections: connections.New(cfg)}).Handler()

	w := do(h, http.MethodPost, "/llm/connections", testToken,
		`{"provider":"deepseek","label":"DeepSeek"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("beklenen 501, alınan %d: %s", w.Code, w.Body.String())
	}
	// Named, so the operator knows which one and that it is not their mistake.
	if !strings.Contains(w.Body.String(), "DeepSeek") {
		t.Errorf("sağlayıcı adı geçmiyor: %s", w.Body.String())
	}
}

func TestAddConnection_RefusesSomethingTheCatalogueNeverHeardOf(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, Deps{Connections: connections.New(cfg)}).Handler()

	w := do(h, http.MethodPost, "/llm/connections", testToken, `{"provider":"uydurma"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("beklenen 404, alınan %d: %s", w.Code, w.Body.String())
	}
}

// The catalogue is published so the picker is the final one: an operator sees
// what is here and what is coming, and adding an adapter later changes a status
// rather than a screen.
func TestListConnections_PublishesTheWholeCatalogue(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, Deps{Connections: connections.New(cfg)}).Handler()

	w := do(h, http.MethodGet, "/llm/connections", testToken, "")
	var got struct {
		Catalogue []struct {
			ID, Label, Vendor, Transport, Auth, Status, Note string
		} `json:"catalogue"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Catalogue) < 10 {
		t.Fatalf("katalog beklenenden kısa: %d", len(got.Catalogue))
	}

	byID := map[string]string{}
	for _, e := range got.Catalogue {
		byID[e.ID] = e.Status
		// A coming-soon entry must say why, and an available one must not
		// pretend to have a reason.
		if e.Status == "coming-soon" && e.Note == "" {
			t.Errorf("%s yakında ama sebebi yazmıyor", e.ID)
		}
	}

	// The duplication that is the point: a subscription and a key to the same
	// vendor are two budgets, and the operator must be able to hold both.
	if byID["claude"] != "available" {
		t.Errorf("claude durumu %q", byID["claude"])
	}
	if byID["anthropic-api"] != "coming-soon" {
		t.Errorf("anthropic-api durumu %q", byID["anthropic-api"])
	}

	// And no coming-soon entry may carry a base URL or a docs URL: those are
	// facts about somebody else's service, and an unverified fact in a table is
	// the failure this whole approach exists to avoid.
	for _, forbidden := range []string{"https://api.", "base_url", "docs_url"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Errorf("katalog doğrulanmamış bir olgu taşıyor (%q)", forbidden)
		}
	}
}
