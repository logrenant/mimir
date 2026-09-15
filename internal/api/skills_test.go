package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/skills"
)

func skillServer(t *testing.T, deps Deps) http.Handler {
	t.Helper()
	return New(testConfig(), deps).Handler()
}

// TestListAgents_IsServedByADaemonWithNothingElseWiredUp is the reason the
// route is registered unconditionally: a picker drawn from an empty list is
// wrong in a way the operator cannot see, while a picker drawn from the
// catalogue is right whatever else is broken.
func TestListAgents_IsServedByADaemonWithNothingElseWiredUp(t *testing.T) {
	w := do(skillServer(t, Deps{}), "GET", "/agents", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got struct {
		Agents []struct {
			Key            string   `json:"key"`
			RequiredSkills []string `json:"required_skills"`
		} `json:"agents"`
		Default string `json:"default"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) != len(agents.Registry()) {
		t.Fatalf("got %d agents, want %d", len(got.Agents), len(agents.Registry()))
	}
	if got.Default != agents.Default {
		t.Fatalf("default = %q, want %q", got.Default, agents.Default)
	}
	for _, a := range got.Agents {
		if len(a.RequiredSkills) == 0 {
			t.Errorf("agent %q reached the wire with no required skill", a.Key)
		}
	}
}

// TestSkillRoutes_AreAbsentWithoutAStore mirrors the package's rule that a
// route exists only when the dependency behind it does.
func TestSkillRoutes_AreAbsentWithoutAStore(t *testing.T) {
	w := do(skillServer(t, Deps{}), "GET", "/skills", testToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestListSkills_ReturnsEveryShippedSkillWithItsBody(t *testing.T) {
	h := skillServer(t, Deps{Skills: skills.New(t.TempDir())})
	w := do(h, "GET", "/skills", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var got struct {
		Skills []struct {
			ID        string `json:"id"`
			Body      string `json:"body"`
			Version   string `json:"version"`
			IsDefault bool   `json:"is_default"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != len(skills.IDs()) {
		t.Fatalf("got %d skills, want %d", len(got.Skills), len(skills.IDs()))
	}
	for _, sk := range got.Skills {
		if strings.TrimSpace(sk.Body) == "" || sk.Version == "" {
			t.Errorf("skill %q reached the editor with no body or no version", sk.ID)
		}
		if !sk.IsDefault {
			t.Errorf("skill %q is not the shipped default on a fresh store", sk.ID)
		}
	}
}

func TestSaveSkill_ThenResetRestoresTheShippedBody(t *testing.T) {
	h := skillServer(t, Deps{Skills: skills.New(t.TempDir())})

	w := do(h, "PUT", "/skills/"+skills.Marketing, testToken, `{"body":"yalnız bunu yaz"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var edited struct {
		Body      string `json:"body"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	if edited.IsDefault || edited.Body != "yalnız bunu yaz" {
		t.Fatalf("edit did not take: %+v", edited)
	}

	w = do(h, "POST", "/skills/"+skills.Marketing+"/reset", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", w.Code)
	}
	var reset struct {
		IsDefault bool `json:"is_default"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reset); err != nil {
		t.Fatal(err)
	}
	if !reset.IsDefault {
		t.Fatal("reset did not restore the shipped body")
	}
}

// TestSaveSkill_RefusesAnIdThisBinaryDoesNotShip — the set is closed, and a
// 404 says so rather than quietly writing a file nothing will ever read.
func TestSaveSkill_RefusesAnIdThisBinaryDoesNotShip(t *testing.T) {
	h := skillServer(t, Deps{Skills: skills.New(t.TempDir())})
	w := do(h, "PUT", "/skills/seo", testToken, `{"body":"x"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
}
