import { describe, expect, test } from "vitest";

import type { AgentCatalogue, Project } from "./daemon";
import {
  agentNeedsProject,
  canStart,
  emptyWizard,
  inferProject,
  nextStep,
  summary,
} from "./wizard";

const project = (id: string, name: string, path: string): Project => ({
  id,
  display_name: name,
  path,
  created_at: "",
  last_used_at: "",
});

const projects: Project[] = [
  project("p1", "mimir-agent", "/Users/x/development/mimir-studio/mimir-agent"),
  project("p2", "api", "/Users/x/development/nd/stralgo/api"),
  project("p3", "İçerik", "/Users/x/development/icerik"),
];

const agents: AgentCatalogue = {
  default: "coding",
  agents: [
    { key: "coding", name: "Kod", desc: "", executor: "claude", required_skills: [], needs_project: true },
    { key: "marketing", name: "Pazarlama", desc: "", executor: "claude", required_skills: [], needs_project: false },
  ],
};

describe("inferProject", () => {
  test("reads the folder out of the sentence", () => {
    const got = inferProject("mimir-agent'ta katalog testlerini düzelt", projects);
    expect(got?.project.id).toBe("p1");
    expect(got?.matched).toBe("mimir-agent");
  });

  // The Turkish case that a plain toLowerCase gets wrong: "İ".toLowerCase() is
  // "i" plus a combining dot under the default locale, so the folder would
  // never match the word the operator typed.
  test("matches a Turkish name whatever case it was typed in", () => {
    expect(inferProject("içerik klasöründe bir şey yap", projects)?.project.id).toBe("p3");
    expect(inferProject("İÇERİK klasöründe bir şey yap", projects)?.project.id).toBe("p3");
  });

  // The false positive the boundary rule exists for. A project called `api`
  // must not be dragged in by a sentence about "apiler", and one called `test`
  // must not be dragged in by "testleri".
  test("does not match a name that is only the start of a longer word", () => {
    const withTest = [...projects, project("p4", "test", "/Users/x/test")];
    expect(inferProject("testleri düzelt", withTest)).toBeNull();
    expect(inferProject("apileri gözden geçir", projects)).toBeNull();
  });

  // Turkish glues its suffixes on with an apostrophe, so an apostrophe has to
  // end a name rather than break the match.
  test("an apostrophe ends the name rather than breaking the match", () => {
    expect(inferProject("api'de testleri koştur", projects)?.project.id).toBe("p2");
  });

  test("the longer name wins when two could match", () => {
    const both = [project("p9", "mimir", "/Users/x/mimir"), ...projects];
    expect(inferProject("mimir-agent'ta bir hata var", both)?.project.id).toBe("p1");
  });

  test("a name shorter than three characters is never a match", () => {
    const short = [project("p5", "ui", "/Users/x/ui")];
    expect(inferProject("ui değişikliği", short)).toBeNull();
  });

  test("says nothing when the sentence names no folder", () => {
    expect(inferProject("bir şeyler düzelt", projects)).toBeNull();
  });
});

describe("nextStep", () => {
  test("asks for the work before anything else", () => {
    expect(nextStep(emptyWizard, agents)).toBe("prompt");
  });

  test("asks for the folder only when the agent needs one", () => {
    const written = { ...emptyWizard, prompt: "bir şey yap" };
    expect(nextStep(written, agents)).toBe("project");
    expect(nextStep({ ...written, agent: "marketing" }, agents)).toBe("confirm");
    expect(nextStep({ ...written, project: "p1" }, agents)).toBe("confirm");
  });

  // Two questions are deliberately not steps. The agent is answered by the
  // daemon's own router (task-77) and asking here would be a second, worse copy
  // of that decision; the model's answer is almost always the default, so it is
  // offered on the confirm step rather than demanded before it.
  test("never asks for a model or an agent", () => {
    const ready = { ...emptyWizard, prompt: "x", project: "p1" };
    expect(nextStep(ready, agents)).toBe("confirm");
    expect(nextStep({ ...ready, model: "" }, agents)).toBe("confirm");
    expect(nextStep({ ...ready, agent: "" }, agents)).toBe("confirm");
  });
});

describe("agentNeedsProject", () => {
  // An unchosen agent resolves to the daemon's own default, which is coding,
  // which is folder-scoped. Guessing the other way would start a run the
  // daemon then rejects.
  test("an unchosen agent is treated as needing a folder", () => {
    expect(agentNeedsProject(agents, "")).toBe(true);
    expect(agentNeedsProject(null, "marketing")).toBe(true);
  });
});

describe("summary", () => {
  test("names the agent, the folder and the model", () => {
    const s = summary(
      { ...emptyWizard, prompt: "x", project: "p1", agent: "coding" },
      projects,
      agents,
      "Sonnet 5",
    );
    expect(s).toContain("Kod");
    expect(s).toContain("mimir-agent");
    expect(s).toContain("Sonnet 5");
  });

  test("says so plainly when nothing was chosen", () => {
    const s = summary({ ...emptyWizard, prompt: "x", agent: "marketing" }, projects, agents, "");
    expect(s).toContain("Pazarlama");
    expect(s).toContain("varsayılan model");
  });
});

describe("canStart", () => {
  test("needs work, and a folder when the agent is folder-scoped", () => {
    expect(canStart(emptyWizard, agents)).toBe(false);
    expect(canStart({ ...emptyWizard, prompt: "  " }, agents)).toBe(false);
    expect(canStart({ ...emptyWizard, prompt: "x" }, agents)).toBe(false);
    expect(canStart({ ...emptyWizard, prompt: "x", project: "p1" }, agents)).toBe(true);
    expect(canStart({ ...emptyWizard, prompt: "x", agent: "marketing" }, agents)).toBe(true);
  });
});
