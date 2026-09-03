package leadgen

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
)

// Which tier answered. Recorded so a later stage — and a reviewer reading the
// database — can tell a free answer from one that cost tokens.
const (
	MethodCache      = "cache"
	MethodRule       = "rule"
	MethodModel      = "model"
	MethodUnresolved = "unresolved"
)

// Result is one company's category and where it came from.
type Result struct {
	PlaceID  string
	Category Category
	Method   string
}

// Classifier is the model tier. *refine.Client satisfies it.
type Classifier interface {
	Classify(ctx context.Context, in refine.ClassifyInput) (refine.ClassifyOutput, error)
}

// Store is the cache tier. *store.Store satisfies it, and a nil Store is legal
// — categorization then costs what it costs, every time (SD-6).
type Store interface {
	GetCategorizations(ctx context.Context, placeIDs []string, version string) (map[string]string, error)
	PutCategorization(ctx context.Context, placeID, version, category, method string) error
}

// Categorizer resolves companies to categories through three tiers, cheapest
// first: the cache, the rule table, then the model.
type Categorizer struct {
	cfg        config.Config
	classifier Classifier
	store      Store
	// sel is the operator's model override for this run. Zero routes by class.
	sel llm.Selection
}

func New(cfg config.Config, classifier Classifier, s Store) *Categorizer {
	return &Categorizer{cfg: cfg, classifier: classifier, store: s}
}

// With returns the same stage bound to one run's model selection.
//
// A copy rather than a parameter on Categorize, because a Categorizer is built
// once at wiring time and shared by every concurrent run: a field somebody
// wrote per request would decide which model another operator's run spends.
// The copy is cheap and lives exactly as long as the run that made it.
func (c *Categorizer) With(sel llm.Selection) *Categorizer {
	if c == nil || sel.IsZero() {
		return c
	}
	cp := *c
	cp.sel = sel
	return &cp
}

// version namespaces the cache by the model that filled it.
//
// Without this a run switched to a different model would be served the last
// model's answers and never call the one that was chosen — the selection would
// look like it did nothing. The zero selection contributes nothing to the
// string, so every categorization cached before this existed stays a hit.
func (c *Categorizer) version() string {
	if key := c.sel.Key(); key != "" {
		return c.cfg.LeadgenCategoryVersion + "@" + key
	}
	return c.cfg.LeadgenCategoryVersion
}

// Categorize returns one Result per company, in input order.
//
// gaps names what could not be resolved and why; it is diagnostic, never a
// reason to fail. A failing cache, a failing subprocess, or a model answer we
// do not recognise all degrade to CategoryUnknown for the companies involved
// (SD-6). Only the caller's own cancellation returns an error.
func (c *Categorizer) Categorize(ctx context.Context, cs []maps.Company) ([]Result, []string, error) {
	results := make([]Result, len(cs))
	if len(cs) == 0 {
		return results, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	var gaps []string

	// Tier 1: one batch read of everything already known at this version.
	cached := map[string]string{}
	if c.store != nil {
		ids := make([]string, 0, len(cs))
		seen := make(map[string]struct{}, len(cs))
		for _, company := range cs {
			if company.PlaceID == "" {
				continue
			}
			if _, dup := seen[company.PlaceID]; dup {
				continue
			}
			seen[company.PlaceID] = struct{}{}
			ids = append(ids, company.PlaceID)
		}
		got, err := c.store.GetCategorizations(ctx, ids, c.version())
		if err != nil {
			gaps = append(gaps, "categorization cache unavailable: "+err.Error())
		} else {
			cached = got
		}
	}

	// Tier 2: the rule table. Everything it cannot answer becomes the residue.
	var residual []maps.Company
	residualSeen := make(map[string]struct{}, len(cs))

	for i, company := range cs {
		if cat, ok := cached[company.PlaceID]; ok && Valid(cat) {
			results[i] = Result{PlaceID: company.PlaceID, Category: Category(cat), Method: MethodCache}
			continue
		}

		// types[] first, then the name. A scraped row has no types at all, so
		// without the second lookup every mapscrape region reached the model —
		// and came back wholly `unknown` whenever its quota was spent.
		cat, ok := CategoryForTypes(company.PrimaryType, company.Types)
		if !ok {
			cat, ok = CategoryForName(company.Name)
		}
		if ok {
			results[i] = Result{PlaceID: company.PlaceID, Category: cat, Method: MethodRule}
			if err := c.remember(ctx, company.PlaceID, cat, MethodRule); err != nil {
				gaps = append(gaps, err.Error())
			}
			continue
		}

		results[i] = Result{PlaceID: company.PlaceID, Category: CategoryUnknown, Method: MethodUnresolved}

		// A company with no place_id can still be classified, it just cannot be
		// cached. One entry per id, so a region that lists the same business
		// twice is paid for once.
		if company.PlaceID == "" {
			continue
		}
		if _, dup := residualSeen[company.PlaceID]; dup {
			continue
		}
		residualSeen[company.PlaceID] = struct{}{}
		residual = append(residual, company)
	}

	if len(residual) == 0 || c.classifier == nil {
		if len(residual) > 0 {
			gaps = append(gaps, fmt.Sprintf("%d companies left unresolved: no classifier configured", len(residual)))
		}
		return results, gaps, nil
	}

	// Tier 3: the residue, batched.
	resolved, batchGaps, err := c.classifyResidual(ctx, residual)
	if err != nil {
		return nil, nil, err
	}
	gaps = append(gaps, batchGaps...)

	for i := range results {
		if results[i].Method != MethodUnresolved {
			continue
		}
		if cat, ok := resolved[results[i].PlaceID]; ok {
			results[i].Category = cat
			results[i].Method = MethodModel
		}
	}

	return results, gaps, nil
}

// classifyResidual runs the model tier in bounded-concurrency batches.
func (c *Categorizer) classifyResidual(ctx context.Context, residual []maps.Company) (map[string]Category, []string, error) {
	batchSize := c.cfg.LeadgenBatchSize
	if batchSize <= 0 {
		batchSize = 20
	}
	limit := c.cfg.MaxConcurrentRefines
	if limit <= 0 {
		limit = 1
	}

	var (
		mu       sync.Mutex
		resolved = make(map[string]Category, len(residual))
		gaps     []string
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for start := 0; start < len(residual); start += batchSize {
		batch := residual[start:min(start+batchSize, len(residual))]

		g.Go(func() error {
			// The group's context is cancelled on the first hard failure; a
			// batch that has not started yet must not spawn a subprocess.
			if err := gctx.Err(); err != nil {
				return err
			}

			out, err := c.classifier.Classify(gctx, refine.ClassifyInput{
				Items:      classifyItems(batch),
				Categories: CategoryStrings(),
				MaxTokens:  c.cfg.LeadgenClassifyMaxTokens,
				Selection:  c.sel,
			})
			if err != nil {
				// A cancelled caller is the one failure that is not a gap: it
				// means nobody is waiting for this answer any more.
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				mu.Lock()
				gaps = append(gaps, fmt.Sprintf("classify batch of %d failed: %v", len(batch), err))
				mu.Unlock()
				return nil
			}

			mu.Lock()
			defer mu.Unlock()
			for _, company := range batch {
				cat, ok := out.Assignments[company.PlaceID]
				if !ok || !Valid(cat) {
					// Absent or outside the vocabulary. Left unresolved on
					// purpose: writing "unknown" here would make one bad answer
					// permanent for this taxonomy version.
					continue
				}
				resolved[company.PlaceID] = Category(cat)
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	// Cache writes happen after the fan-out, on one goroutine: SQLite is a
	// single file and these rows are small, so serialising them keeps the write
	// path simple and the store's busy_timeout out of it.
	for placeID, cat := range resolved {
		if err := c.remember(ctx, placeID, cat, MethodModel); err != nil {
			gaps = append(gaps, err.Error())
		}
	}

	return resolved, gaps, nil
}

// classifyItems flattens a batch into what the refiner sees. Everything here
// except the id is untrusted provider text; internal/refine fences and bounds
// it.
func classifyItems(batch []maps.Company) []refine.ClassifyItem {
	items := make([]refine.ClassifyItem, 0, len(batch))
	for _, company := range batch {
		types := company.Types
		if company.PrimaryType != "" {
			types = append([]string{company.PrimaryType}, types...)
		}
		items = append(items, refine.ClassifyItem{
			ID:      company.PlaceID,
			Name:    company.Name,
			Types:   strings.Join(types, " "),
			Address: company.FormattedAddress,
		})
	}
	return items
}

// remember writes one resolved category back to the cache. A store that cannot
// be written is a gap, never a failure: the answer in hand is still correct.
func (c *Categorizer) remember(ctx context.Context, placeID string, cat Category, method string) error {
	if c.store == nil || placeID == "" {
		return nil
	}
	if err := c.store.PutCategorization(ctx, placeID, c.version(), string(cat), method); err != nil {
		return fmt.Errorf("caching category for %s: %w", placeID, err)
	}
	return nil
}
