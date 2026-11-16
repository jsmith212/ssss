package index

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"ssss/internal/model"
)

func expect(t *testing.T, ix *Index, query string, want ...string) {
	t.Helper()
	if got := matches(t, ix, query); !reflect.DeepEqual(got, sorted(want...)) {
		t.Errorf("%q matched %v, want %v", query, got, sorted(want...))
	}
}

// The sidebar counts per pack id, so a filter written with the id has to
// select that one pack even when another pack has the same name, or is
// named like the id.
func TestPackAndCollectionByIDFirst(t *testing.T) {
	ix := openTemp(t)
	one := Pack{ID: "pk_1", Name: "Drums"}
	two := Pack{ID: "pk_2", Name: "Drums"}
	odd := Pack{ID: "pk_3", Name: "pk_1"} // named like another pack's id
	for _, c := range []struct {
		p  Pack
		id string
	}{{one, "a"}, {two, "b"}, {odd, "c"}} {
		if err := ix.ReplacePack(c.p, inPack(c.p, []Sample{sample(c.id, c.id+".wav", "oneshot", 1, 0, "", 1)})); err != nil {
			t.Fatal(err)
		}
	}
	err := ix.ReplaceCollections([]Collection{
		{ID: "col_1", Name: "Set", Items: []string{"a"}},
		{ID: "col_2", Name: "Set", Items: []string{"b"}},
		{ID: "col_3", Name: "col_1", Items: []string{"c"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "pack:pk_1", "a")
	expect(t, ix, "pack:pk_2", "b")
	expect(t, ix, "pack:pk_3", "c")
	expect(t, ix, "@pk_1", "a")
	expect(t, ix, "pack:Drums", "a", "b") // a typed name means every pack called that
	expect(t, ix, "-pack:pk_1", "b", "c")
	expect(t, ix, "in:col_1", "a")
	expect(t, ix, "in:col_3", "c")
	expect(t, ix, "in:set", "a", "b")
	expect(t, ix, "pack:pk_1 in:col_1", "a")
	expect(t, ix, "pack:pk_2 in:col_1")

	sb, err := ix.Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range sb.Packs {
		if got := len(matches(t, ix, "pack:"+p.ID)); got != p.Count {
			t.Errorf("pack %s: sidebar says %d, its filter lists %d", p.ID, p.Count, got)
		}
	}
}

// One- and two-character terms cannot use the trigram index; they must still
// ignore case beyond ASCII, in paths, tags and pack names alike.
func TestShortTermsFoldCaseBeyondASCII(t *testing.T) {
	ix := openTemp(t)
	p := Pack{ID: "pk_1", Name: "ÄÖ Sounds"}
	q := Pack{ID: "pk_2", Name: "Other"}
	if err := ix.ReplacePack(p, inPack(p, []Sample{sample("plain", "x.wav", "oneshot", 1, 0, "", 1)})); err != nil {
		t.Fatal(err)
	}
	err := ix.ReplacePack(q, inPack(q, []Sample{
		sample("path", "Été/Çà_01.wav", "oneshot", 1, 0, "", 2),
		sample("tag", "y.wav", "oneshot", 1, 0, "", 3, "Ünique/Ж"),
		sample("ascii", "Zed_Q7.wav", "oneshot", 1, 0, "", 4),
	}))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "é", "path")
	expect(t, ix, "É", "path")
	expect(t, ix, "çà", "path")
	expect(t, ix, "ÇÀ", "path")
	expect(t, ix, "ж", "tag")
	expect(t, ix, "Ж", "tag")
	expect(t, ix, "ü", "tag")
	expect(t, ix, "äö", "plain")
	expect(t, ix, "ÄÖ", "plain")
	expect(t, ix, "q7", "ascii")
	expect(t, ix, "Q7", "ascii")
	expect(t, ix, "-É", "plain", "tag", "ascii")
	// Longer terms go through the trigram index, which folds case itself.
	expect(t, ix, "ÉTÉ", "path")
	expect(t, ix, "ünique", "tag")

	// The shadow columns follow a rename and a retag.
	moved := inPack(q, []Sample{sample("path", "Plain/Name.wav", "oneshot", 1, 0, "", 2, "Œuvre")})
	if err := ix.UpsertSamples(moved, 0); err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "é")
	expect(t, ix, "œ", "path")
	expect(t, ix, "Œ", "path")
}

// A pasted or generated query must not become a statement SQLite refuses.
func TestParseCapsTermsAndLength(t *testing.T) {
	q, err := Parse(strings.Repeat("kick ", 3000))
	if len(q.Terms) != maxQueryTerms {
		t.Errorf("%d terms, want %d", len(q.Terms), maxQueryTerms)
	}
	if err == nil || !strings.Contains(err.Error(), "too many terms") {
		t.Errorf("error %v does not say there were too many terms", err)
	}
	q, err = Parse("kick " + strings.Repeat("é", maxQueryBytes))
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("error %v does not say the query was too long", err)
	}
	if len(q.Terms) != 2 || q.Terms[0].Text != "kick" || len(q.Terms[1].Text) > maxQueryBytes {
		t.Errorf("terms %d", len(q.Terms))
	}
	if q, err := Parse(strings.Repeat("a ", maxQueryTerms)); err != nil || len(q.Terms) != maxQueryTerms {
		t.Errorf("a query at the limit: %d terms, %v", len(q.Terms), err)
	}

	ix := openFixture(t)
	for _, query := range []string{
		strings.Repeat("kick ", 3000),
		strings.Repeat("-a ", 3000),
		strings.Repeat("ki ", 3000),
		strings.Repeat("bpm:120 key:compatible:Am #kick ", 1500),
		strings.Repeat("x", 1<<20),
	} {
		res, err := ix.Search(model.SearchRequest{Query: query})
		if err != nil {
			t.Errorf("search of %d bytes: %v", len(query), err)
		}
		if res.Error == "" {
			t.Errorf("search of %d bytes reported no problem", len(query))
		}
		if _, err := ix.SearchIDs(query, "", false); err != nil {
			t.Errorf("ids of %d bytes: %v", len(query), err)
		}
	}
	res, err := ix.Search(model.SearchRequest{Query: strings.Repeat("kick ", 3000)})
	if err != nil || res.Total != 3 {
		t.Errorf("capped query found %d (%v), want the 3 kicks", res.Total, err)
	}
}

// A cache without import batches (rebuilt, or filled from a library another
// machine imported into) derives Recent from the added times.
func TestDeriveRecent(t *testing.T) {
	const minute = 60_000
	ix := openTemp(t)
	p := Pack{ID: "pk_1", Name: "P"}
	at := func(id string, added int64) Sample { return sample(id, id+".wav", "oneshot", 1, 0, "", added) }
	old := []Sample{at("o1", 1*minute), at("o2", 2*minute)}
	last := []Sample{at("n1", 60*minute), at("n2", 63*minute), at("n3", 67*minute), at("n4", 67*minute)}
	undated := at("zero", 0)
	if err := ix.ReplacePack(p, inPack(p, append(append(old, last...), undated))); err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "is:recent")
	if err := ix.DeriveRecent(); err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "is:recent", "n1", "n2", "n3", "n4")
	if sb, err := ix.Sidebar(nil, nil); err != nil || sb.Recent != 4 {
		t.Errorf("sidebar recent = %d (%v), want 4", sb.Recent, err)
	}

	// A real import afterwards takes over, and deriving again leaves it be.
	batch, err := ix.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.UpsertSamples(inPack(p, []Sample{at("imp", 90*minute)}), batch); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeriveRecent(); err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "is:recent", "imp")

	// Without any gap the whole library is one batch; without samples
	// nothing happens.
	one := openTemp(t)
	if err := one.DeriveRecent(); err != nil {
		t.Fatal(err)
	}
	if err := one.ReplacePack(p, inPack(p, []Sample{at("a", minute), at("b", 2*minute)})); err != nil {
		t.Fatal(err)
	}
	if err := one.DeriveRecent(); err != nil {
		t.Fatal(err)
	}
	expect(t, one, "is:recent", "a", "b")
}

// A root note without a mode fits every key on that tonic.
func TestKeySearchWithRootOnlyKeys(t *testing.T) {
	ix := openTemp(t)
	p := Pack{ID: "pk_1", Name: "P"}
	root := func(id, key string) Sample {
		s := sample(id, id+".wav", "oneshot", 1, 0, key, 1)
		s.KeyRoot, s.KeySrc = true, "detected"
		return s
	}
	err := ix.ReplacePack(p, inPack(p, []Sample{
		root("rootA", "A"), root("rootC", "C"),
		sample("majA", "majA.wav", "loop", 1, 0, "A", 1),
		sample("minA", "minA.wav", "loop", 1, 0, "Am", 1),
		sample("majC", "majC.wav", "loop", 1, 0, "C", 1),
	}))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "key:A", "rootA", "majA")
	expect(t, ix, "key:Am", "rootA", "minA")
	expect(t, ix, "key:Cm", "rootC")
	expect(t, ix, "key:compatible:Am", "rootA", "minA", "rootC", "majC")
	expect(t, ix, "-key:Am", "rootC", "majA", "majC")
	rows, err := ix.Samples([]string{"rootA", "majA"})
	if err != nil || len(rows) != 2 || !rows[0].KeyRoot || rows[1].KeyRoot {
		t.Errorf("key root flag did not round-trip: %+v %v", rows, err)
	}
	// The flag follows an update.
	rows[0].KeyRoot = false
	if err := ix.UpsertSamples(rows[:1], 0); err != nil {
		t.Fatal(err)
	}
	expect(t, ix, "key:Am", "minA")
}

// Damage that leaves the header readable is found by Check, not by whichever
// query later reads the bad page.
func TestCheckFindsDamagedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := Pack{ID: "pk_1", Name: "P"}
	var many []Sample
	for i := range 2000 {
		id := "s" + strings.Repeat("x", 40) + string(rune('a'+i%26)) + strings.Repeat("y", i%50) + "_" + strconv.Itoa(i)
		many = append(many, sample(id, "Some/Folder/"+id+".wav", "oneshot", 1, 0, "", int64(i)))
	}
	if err := ix.ReplacePack(p, inPack(p, many)); err != nil {
		t.Fatal(err)
	}
	if err := ix.Close(); err != nil {
		t.Fatal(err)
	}
	// Checkpointed on close: the data is in the main file. Scribble over
	// pages in the middle, past the header and the schema.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 64*4096 {
		t.Fatalf("database only %d bytes; the test needs more pages", len(raw))
	}
	for i := 20 * 4096; i < 40*4096; i++ {
		raw[i] = byte(i * 31)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")

	again, err := Open(path)
	if err != nil {
		t.Fatalf("Open of a damaged database: %v", err)
	}
	defer again.Close()
	if err := again.Check(); !errors.Is(err, ErrDamaged) {
		t.Errorf("Check of a damaged database: %v", err)
	}
	sound := openFixture(t)
	if err := sound.Check(); err != nil {
		t.Errorf("Check of a sound database: %v", err)
	}
}

// Write transactions take their lock up front, so two processes sharing a
// cache wait for each other instead of failing.
func TestWritersInTwoProcessesWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path) // a second connection pool stands in for a second process
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p := Pack{ID: "pk_1", Name: "P"}
	if err := a.UpsertPack(p); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for n, ix := range []*Index{a, b} {
		go func() {
			var err error
			for i := 0; i < 150 && err == nil; i++ {
				id := strconv.Itoa(n) + "_" + strconv.Itoa(i)
				// Read inside the transaction before writing, which is
				// where a deferred transaction would fail to upgrade.
				err = ix.UpsertSamples(inPack(p, []Sample{sample(id, id+".wav", "oneshot", 1, 0, "", 1)}), 0)
			}
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("concurrent writer: %v", err)
		}
	}
	if n, err := a.Count(); err != nil || n != 300 {
		t.Errorf("count = %d, %v", n, err)
	}
	var mode string
	if err := a.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal mode %q, %v", mode, err)
	}
}
