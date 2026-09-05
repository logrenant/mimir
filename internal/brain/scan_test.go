package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
)

// fakeHashes stands in for the store's "what have I already read" lookup.
type fakeHashes struct{ m map[string]string }

func (f *fakeHashes) BrainNodeHashes(context.Context, string, string, string) (map[string]string, error) {
	return f.m, nil
}

// scanRepo writes a small tree: three files worth reading and four that are
// not, one of each reason.
func scanRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("main.go", "package main\n\nfunc main() {}\n")
	write("README.md", "# Title\n\nSome prose.\n")
	write("internal/store/brain.go", "package store\n")

	write("go.sum", "example.com/x v1.0.0 h1:deadbeef\n")              // generated
	write("internal/refine/testdata/big.golden", "a golden fixture\n") // a fixture, twice over
	write("assets/font.woff2", "not really a font but the name is")    // binary by extension
	write("bundle.min.js", "var a=1;")                                 // generated

	// Binary by content, with an extension the list does not name.
	if err := os.WriteFile(filepath.Join(dir, "blob.dat"), []byte{'a', 0x00, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func scanCore(t *testing.T, st Store, model Completer) *Core {
	t.Helper()
	cfg := config.Load()
	cfg.BrainScanBatch = 2
	return New(cfg, st, model)
}

// A dry run answers "what would this cost" and must spend nothing. The core is
// built with a nil Completer so a model call panics rather than quietly
// starting to bill.
func TestScan_DryRunCostsNothing(t *testing.T) {
	dir := scanRepo(t)
	c := scanCore(t, newFakeStore(), nil)

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !got.DryRun || got.Scanned != 0 {
		t.Fatalf("dry run scanned %d files: %+v", got.Scanned, got)
	}
	if got.Eligible != 3 {
		t.Fatalf("eligible = %d, want the 3 real files; got %v", got.Eligible, got.Files)
	}
	// The whole pending set is reported, not just one batch: the point of a dry
	// run is knowing the size of the bill.
	if got.Remaining != 3 {
		t.Errorf("remaining = %d, want 3", got.Remaining)
	}

	joined := strings.Join(got.Files, ",")
	for _, unwanted := range []string{"go.sum", "golden", "woff2", "min.js", "blob.dat"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("%s should not be eligible: %v", unwanted, got.Files)
		}
	}
}

func TestScan_DistilsABatchAndReportsWhatIsLeft(t *testing.T) {
	dir := scanRepo(t)
	st := newFakeStore()
	model := &fakeLLM{distil: goodDistil, relate: `{"related":[]}`}
	c := scanCore(t, st, model)

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.Scanned != 2 || got.Remaining != 1 {
		t.Fatalf("stats = %+v, want 2 scanned and 1 remaining", got)
	}
	if len(st.nodes) != 2 {
		t.Fatalf("stored %d nodes, want 2", len(st.nodes))
	}
	for _, n := range st.nodes {
		if n.Kind != KindFile {
			t.Errorf("kind = %q, want file", n.Kind)
		}
		if n.ContentHash == "" {
			t.Error("a scanned node has no content hash; a re-scan would pay for it again")
		}
	}
}

// The property the whole feature rests on: a second scan of an unchanged
// repository makes no model call.
func TestScan_UnchangedFilesAreFree(t *testing.T) {
	dir := scanRepo(t)
	st := newFakeStore()
	c := scanCore(t, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	first, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.Scanned != 3 {
		t.Fatalf("first scan covered %d files, want 3", first.Scanned)
	}

	// Feed the stored hashes back, the way the store would.
	known := map[string]string{}
	for _, n := range st.nodes {
		known[n.SourceKey] = n.ContentHash
	}

	// A nil Completer: the second pass must not reach a model at all.
	second := New(c.cfg, st, nil)
	got, err := second.Scan(context.Background(), dir, &fakeHashes{m: known}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if got.Scanned != 0 || got.Remaining != 0 {
		t.Fatalf("second scan = %+v, want nothing to do", got)
	}
	if got.Skipped != 3 {
		t.Errorf("skipped = %d, want all 3", got.Skipped)
	}
}

// A file that changed after being scanned has to come back.
func TestScan_ChangedFileIsRescanned(t *testing.T) {
	dir := scanRepo(t)
	st := newFakeStore()
	c := scanCore(t, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	if _, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	known := map[string]string{}
	for _, n := range st.nodes {
		known[n.SourceKey] = n.ContentHash
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := c.Scan(context.Background(), dir, &fakeHashes{m: known}, ScanOptions{Limit: 50, DryRun: true})
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if got.Remaining != 1 || len(got.Files) != 1 || got.Files[0] != "README.md" {
		t.Errorf("changed file not detected: %+v", got)
	}
}

func TestScan_TooLargeIsSkipped(t *testing.T) {
	dir := scanRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "huge.md"), []byte(strings.Repeat("x", 200<<10)), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.BrainScanMaxFileBytes = 100 << 10
	c := New(cfg, newFakeStore(), nil)

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if strings.Contains(strings.Join(got.Files, ","), "huge.md") {
		t.Errorf("an oversized file was eligible: %v", got.Files)
	}
}

// There is no default project, ever.
func TestScan_RefusesWithoutAProject(t *testing.T) {
	c := scanCore(t, newFakeStore(), nil)
	if _, err := c.Scan(context.Background(), "", &fakeHashes{}, ScanOptions{}); err == nil {
		t.Fatal("a scan with no project path must be an error")
	}
}

func TestScan_WithoutAStore(t *testing.T) {
	c := New(config.Load(), nil, nil)
	if _, err := c.Scan(context.Background(), "/tmp", &fakeHashes{}, ScanOptions{}); err == nil {
		t.Fatal("expected ErrNoStore")
	}
}

func TestScannable(t *testing.T) {
	cases := map[string]bool{
		"internal/brain/scan.go":               true,
		"README.md":                            true,
		"docs/ROADMAP.md":                      true,
		"go.sum":                               false,
		"desktop/package-lock.json":            false,
		"internal/refine/testdata/page.golden": false,
		"internal/refine/testdata/anything.md": false,
		"desktop/src/assets/fonts/x.woff2":     false,
		"node_modules/left-pad/index.js":       false,
		"dist/bundle.min.js":                   false,
		"icons/icon.icns":                      false,
	}
	for in, want := range cases {
		if got := scannable(in); got != want {
			t.Errorf("scannable(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsText(t *testing.T) {
	if !isText([]byte("package main\n")) {
		t.Error("plain source is not text?")
	}
	if isText([]byte{'a', 0x00, 'b'}) {
		t.Error("a NUL byte should mark it binary")
	}
	if isText([]byte{0xff, 0xfe, 0xfd}) {
		t.Error("invalid UTF-8 should mark it binary")
	}
}

// A provider that was down is not a file that was read. The node is kept —
// a titled node is still findable — but without a content hash, so the next
// pass offers the file again instead of skipping it forever on the strength of
// one hiccup. This is what keeps a machine-wide run honest: it cannot report a
// clean sweep over a stretch where nothing was distilled.
func TestScan_UndistilledFilesAreRetriedNotSkipped(t *testing.T) {
	dir := scanRepo(t)
	st := newFakeStore()
	model := &fakeLLM{distilErr: errors.New("agy is out of quota"), relate: `{"related":[]}`}
	c := scanCore(t, st, model)

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.Scanned != 0 || got.Failed != 3 {
		t.Fatalf("stats = %+v, want nothing scanned and 3 failed", got)
	}
	for _, n := range st.nodes {
		if n.ContentHash != "" {
			t.Errorf("%s kept a content hash without an assessment; the next scan would skip it", n.SourceKey)
		}
	}

	// The store now holds three nodes, and every one of them is still pending.
	known := map[string]string{}
	for _, n := range st.nodes {
		known[n.SourceKey] = n.ContentHash
	}
	again, err := c.Scan(context.Background(), dir, &fakeHashes{m: known}, ScanOptions{DryRun: true})
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if again.Remaining != 3 {
		t.Errorf("remaining = %d, want all 3 offered again", again.Remaining)
	}
}

// A changed file and a new one are both Scanned. Telling them apart is what
// lets the console say the detection is working — an operator who edits a
// document and sees it named there knows Brain re-read it.
func TestScan_ChangedIsCountedApartFromNew(t *testing.T) {
	dir := scanRepo(t)
	st := newFakeStore()
	c := scanCore(t, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	first, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.Changed != 0 {
		t.Fatalf("a first sighting is not a change: %+v", first)
	}

	known := map[string]string{}
	for _, n := range st.nodes {
		known[n.SourceKey] = n.ContentHash
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brand-new.md"), []byte("# New\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := c.Scan(context.Background(), dir, &fakeHashes{m: known}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if got.Scanned != 2 {
		t.Fatalf("want both files scanned, got %d", got.Scanned)
	}
	if got.Changed != 1 || len(got.ChangedFiles) != 1 || got.ChangedFiles[0] != "README.md" {
		t.Errorf("only the edited file is a change: %+v", got)
	}
}

// The version row records the file as the scanner found it, so the size and
// mtime have to survive the trip from os.Stat to the store.
func TestScan_CarriesTheFilesShapeToTheStore(t *testing.T) {
	dir := scanRepo(t)
	st := newFakeStore()
	c := scanCore(t, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	if _, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, n := range st.nodes {
		if n.Kind != KindFile {
			continue
		}
		if n.SizeBytes <= 0 || n.ModifiedAt.IsZero() {
			t.Fatalf("%s lost its size or mtime: %+v", n.SourceKey, n)
		}
	}
}

// --- the scan permission surface ---------------------------------------------

// An excluded path must never be opened. Asserted on the result rather than on
// a mock, because the guarantee being made is about a file that is otherwise
// perfectly eligible: it has to fall out of the pending set, not merely fail to
// be distilled.
func TestScan_ExcludedFilesAreNeverRead(t *testing.T) {
	dir := scanRepo(t)
	c := scanCore(t, newFakeStore(), nil)

	all, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if all.Eligible != 3 {
		t.Fatalf("baseline eligible = %d, want 3", all.Eligible)
	}

	// One file by name, and one whole subtree.
	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{
		DryRun: true,
		Exclude: NewExcluder([]string{
			filepath.Join(dir, "README.md"),
			filepath.Join(dir, "internal"),
		}),
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got.Eligible != 1 {
		t.Errorf("eligible = %d, want 1 — only main.go survives", got.Eligible)
	}
	if got.Excluded != 2 {
		t.Errorf("excluded = %d, want 2", got.Excluded)
	}
	joined := strings.Join(got.Files, ",")
	for _, gone := range []string{"README.md", "internal/store/brain.go"} {
		if strings.Contains(joined, gone) {
			t.Errorf("%s was excluded but still appears in %v", gone, got.Files)
		}
	}
	if !strings.Contains(joined, "main.go") {
		t.Errorf("main.go was not excluded but is missing from %v", got.Files)
	}
}

// The shipped credential denylist applies with no policy configured at all —
// that is the whole point of it being separate from the operator's list.
func TestScan_CredentialsAreRefusedWithoutAnyPolicy(t *testing.T) {
	dir := scanRepo(t)
	for rel, body := range map[string]string{
		".env":                   "STRIPE_KEY=sk_live_notreal\n",
		"deploy/server.pem":      "-----BEGIN PRIVATE KEY-----\nnope\n",
		"deploy/id_rsa":          "-----BEGIN OPENSSH PRIVATE KEY-----\nnope\n",
		"infra/terraform.tfvars": "password = \"hunter2\"\n",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c := scanCore(t, newFakeStore(), nil)
	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got.Eligible != 3 {
		t.Errorf("eligible = %d, want the same 3 as before the credentials were written; got %v",
			got.Eligible, got.Files)
	}
	joined := strings.Join(got.Files, ",")
	for _, secret := range []string{".env", "server.pem", "id_rsa", "tfvars"} {
		if strings.Contains(joined, secret) {
			t.Errorf("a credential reached the eligible set: %q in %v", secret, got.Files)
		}
	}
}
