package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/mapscrape"
	"github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/memory"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
)

type diagnosticsTool struct {
	cfg          config.Config
	crawlClient  *crawl.Client
	refineClient *refine.Client
	searchClient *search.Client
	mapsScraper  *mapscrape.Client
	mem          *memory.Memory
}

type diagDependency struct {
	Ok     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Optional marks a dependency the binary runs fine without, so a consumer
	// reading this does not treat a deliberately-unstarted container as a
	// broken install. The Maps scrape sidecar is the only one today: it is the
	// Places API's fallback, useful when it is running and absent otherwise.
	Optional     bool  `json:"optional,omitempty"`
	ModelPresent *bool `json:"model_present,omitempty"`
}

type diagVersions struct {
	Binary         string `json:"binary"`
	MCPSDK         string `json:"mcp_sdk"`
	Crawl4AIImage  string `json:"crawl4ai_image"`
	MapScrapeImage string `json:"mapscrape_image"`
	ClaudeModel    string `json:"claude_model"`
	DistillModel   string `json:"distill_model"`
}

// diagMemory reports whether the project memory has anything in it for the
// directory this process is running in. Omitted entirely when there is no
// memory, rather than reported as zeroes: absent and empty are different
// answers, and only one of them means "the store would not open".
type diagMemory struct {
	Project   string `json:"project"`
	Episodes  int    `json:"episodes"`
	Distilled int    `json:"distilled"`
	Notes     int    `json:"notes"`
	Oldest    string `json:"oldest,omitempty"`
	Newest    string `json:"newest,omitempty"`
}

type diagnosticsResponse struct {
	Crawl4AI diagDependency `json:"crawl4ai"`

	// The distil tier and the reason tier, separately, because since task-51
	// they fail separately: agy has no fallback, so a healthy claude says
	// nothing about whether a page can be summarised or a file distilled. The
	// `claude` key keeps its literal meaning — the refiner's own health — and
	// `agy` is the one an operator now has to look at first.
	Agy         diagDependency `json:"agy"`
	Claude      diagDependency `json:"claude"`
	PDFText     diagDependency `json:"pdftotext"`
	DuckDuckGo  diagDependency `json:"duckduckgo"`
	MapsScraper diagDependency `json:"maps_scraper"`
	Versions    diagVersions   `json:"versions"`
	Memory      *diagMemory    `json:"memory,omitempty"`
}

// We implement mcp.MetadataResponse to pass the choke-point.
func (d diagnosticsResponse) MetadataOnly() bool    { return true }
func (d diagnosticsResponse) SizeBudgetTokens() int { return 500 }

// NewDiagnostics returns the diagnostics tool.
// mem may be nil: no store means no memory, and the report simply omits it.
func NewDiagnostics(cfg config.Config, crawlClient *crawl.Client, refineClient *refine.Client, searchClient *search.Client, mem *memory.Memory) mcp.Tool {
	return &diagnosticsTool{
		cfg:          cfg,
		crawlClient:  crawlClient,
		refineClient: refineClient,
		searchClient: searchClient,
		mem:          mem,
		// Built here rather than passed in, for the same reason RegisterAll
		// builds the Places client itself: it is a thin HTTP caller over cfg
		// with no lifetime to own, and threading it through Deps would make
		// both binaries repeat one construction.
		mapsScraper: mapscrape.New(cfg),
	}
}

func (t *diagnosticsTool) Name() string {
	return "diagnostics"
}

func (t *diagnosticsTool) Description() string {
	return "Checks the health of Mimir's dependencies (Crawl4AI, the claude CLI, DuckDuckGo, the optional Maps scrape sidecar) and reports system state."
}

func (t *diagnosticsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"additionalProperties": false
	}`)
}

func (t *diagnosticsTool) Handle(ctx context.Context, _ json.RawMessage) (any, error) {
	// SD-3: Overall deadline for diagnostics.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	g, ctxGroup := errgroup.WithContext(ctx)

	res := diagnosticsResponse{
		Versions: diagVersions{
			Binary:         mcp.Version,
			MCPSDK:         mcp.SDKVersion,
			Crawl4AIImage:  crawl.ImageTag,
			MapScrapeImage: mapscrape.ImageTag,
			ClaudeModel:    t.cfg.ClaudeModel,
			DistillModel:   t.cfg.DistillModel,
		},
	}

	g.Go(func() error {
		err := t.crawlClient.Health(ctxGroup)
		if err != nil {
			res.Crawl4AI = diagDependency{Ok: false, Detail: err.Error()}
		} else {
			res.Crawl4AI = diagDependency{Ok: true, Detail: ""}
		}
		return nil
	})

	g.Go(func() error {
		ok, err := t.refineClient.Health(ctxGroup)
		res.Claude = diagDependency{Ok: ok}
		if err != nil {
			res.Claude.Detail = err.Error()
		}
		return nil
	})

	g.Go(func() error {
		// The distil tier itself, asked directly rather than through the
		// refiner: `agy models` is the cheapest call that proves both that the
		// binary runs and that it is signed in, and it is now the single point
		// of failure for every summary, every recap and every scanned file.
		p := llm.NewRouter(t.cfg).Provider(llm.Distill)
		if p == nil {
			res.Agy = diagDependency{Ok: false, Detail: "no distil provider configured"}
			return nil
		}
		if err := p.Health(ctxGroup); err != nil {
			res.Agy = diagDependency{Ok: false, Detail: err.Error()}
		} else {
			res.Agy = diagDependency{Ok: true}
		}
		return nil
	})

	g.Go(func() error {
		// Optional in the same sense the Maps sidecar is: without poppler,
		// PDFs are skipped and everything else still gets read. Reporting it
		// as a break would paint a deliberately-poppler-less machine as broken.
		res.PDFText = diagDependency{Optional: true}
		bin, err := exec.LookPath(t.cfg.BrainScanPDFPath)
		if err != nil {
			res.PDFText.Detail = "pdftotext not found — PDFs are skipped (`brew install poppler` to index them)"
			return nil
		}
		probeCtx, cancel := context.WithTimeout(ctxGroup, 2*time.Second)
		defer cancel()
		if err := exec.CommandContext(probeCtx, bin, "-v").Run(); err != nil {
			res.PDFText.Detail = err.Error()
			return nil
		}
		res.PDFText.Ok = true
		return nil
	})

	g.Go(func() error {
		err := t.searchClient.Health(ctxGroup)
		if err != nil {
			res.DuckDuckGo = diagDependency{Ok: false, Detail: err.Error()}
		} else {
			res.DuckDuckGo = diagDependency{Ok: true, Detail: ""}
		}
		return nil
	})

	g.Go(func() error {
		// Down is a normal state for this one — it is the Places fallback, not
		// a hard dependency — so it is reported, with the message that names
		// `make maps-up`, and marked optional rather than presented as a break.
		ok, err := t.mapsScraper.Health(ctxGroup)
		res.MapsScraper = diagDependency{Ok: ok, Optional: true}
		if err != nil {
			res.MapsScraper.Detail = err.Error()
		}
		return nil
	})

	_ = g.Wait()

	res.Memory = t.memoryStats(ctx)

	return res, nil
}

// memoryStats reports on the working directory's memory. Every failure here is
// silent and yields nil: diagnostics exists to say what is wrong with the
// dependencies, and it would be a poor health check that failed because one of
// the things it reports on is unhealthy.
func (t *diagnosticsTool) memoryStats(ctx context.Context) *diagMemory {
	if t.mem == nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	path, err := project.Canonicalize(cwd)
	if err != nil {
		return nil
	}
	st, err := t.mem.Stats(ctx, memory.Project{Path: path})
	if err != nil {
		return nil
	}

	out := &diagMemory{
		Project:   path,
		Episodes:  st.Episodes,
		Distilled: st.WithSummary,
		Notes:     st.Notes,
	}
	if st.OldestEpisode > 0 {
		out.Oldest = time.Unix(st.OldestEpisode, 0).UTC().Format(time.DateOnly)
	}
	if st.NewestEpisode > 0 {
		out.Newest = time.Unix(st.NewestEpisode, 0).UTC().Format(time.DateOnly)
	}
	return out
}
