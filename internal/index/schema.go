package index

// schemaVersion is stored in PRAGMA user_version. The index is a cache of the
// library's JSON files, so a version change throws the file away instead of
// migrating it. Bump it on any change to schema below or to how rows are
// derived from their inputs.
const schemaVersion = 2

// sample.rid is the join key for everything per-sample (and the FTS rowid);
// the text id is only looked up at the edges. The wide mini-peak strings live
// in their own table so filter scans over sample stay narrow.
//
// sample_tag holds the closure of each sample's tags: a sample tagged
// "drums/kick" has rows for "drums" and "drums/kick". That makes "tag or any
// descendant" an equality lookup and makes the sidebar's descendant-inclusive
// counts a plain GROUP BY that counts each sample once per ancestor.
const schema = `
CREATE TABLE meta(k TEXT PRIMARY KEY, v INTEGER NOT NULL) WITHOUT ROWID;

CREATE TABLE pack(
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  name_key   TEXT NOT NULL,
  dir        TEXT NOT NULL,
  source     TEXT NOT NULL,
  added      INTEGER NOT NULL,
  file_mtime INTEGER NOT NULL,
  file_size  INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE sample(
  rid      INTEGER PRIMARY KEY,
  id       TEXT NOT NULL UNIQUE,
  pack_id  TEXT NOT NULL,
  pack_key TEXT NOT NULL, -- pack.name_key, copied here so sorting by pack is an index walk
  name     TEXT NOT NULL,
  ext      TEXT NOT NULL,
  relpath  TEXT NOT NULL,
  -- relpath and tags lower-cased the way query terms are, for the terms too
  -- short for the trigram index: LIKE itself only folds ASCII.
  relpath_key TEXT NOT NULL,
  tags_key    TEXT NOT NULL,
  size     INTEGER NOT NULL,
  dur      REAL NOT NULL,
  sr       INTEGER NOT NULL,
  ch       INTEGER NOT NULL,
  type     TEXT NOT NULL,
  bpm      REAL NOT NULL,
  bpm_src  TEXT NOT NULL,
  key      TEXT NOT NULL,
  key_src  TEXT NOT NULL,
  key_ord  INTEGER NOT NULL,
  key_root INTEGER NOT NULL, -- key is a root note only, mode unknown
  tags     TEXT NOT NULL,
  fav      INTEGER NOT NULL,
  added    INTEGER NOT NULL,
  missing  INTEGER NOT NULL,
  analyzed INTEGER NOT NULL,
  note     TEXT NOT NULL,
  batch    INTEGER NOT NULL
);
CREATE INDEX sample_pack  ON sample(pack_id);
CREATE INDEX sample_bypack ON sample(pack_key, pack_id, relpath COLLATE NOCASE);
CREATE INDEX sample_added ON sample(added);
CREATE INDEX sample_name  ON sample(name COLLATE NOCASE);
CREATE INDEX sample_batch ON sample(batch);

CREATE TABLE sample_mini(rid INTEGER PRIMARY KEY, mini TEXT NOT NULL);

CREATE TABLE tag(
  key  TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  leaf TEXT NOT NULL
) WITHOUT ROWID;
CREATE INDEX tag_leaf ON tag(leaf);

CREATE TABLE sample_tag(
  key TEXT NOT NULL,
  rid INTEGER NOT NULL,
  PRIMARY KEY(key, rid)
) WITHOUT ROWID;
CREATE INDEX sample_tag_rid ON sample_tag(rid);

CREATE TABLE collection(
  id       TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  name_key TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE collection_item(
  coll_id   TEXT NOT NULL,
  sample_id TEXT NOT NULL,
  pos       INTEGER NOT NULL,
  PRIMARY KEY(coll_id, sample_id)
) WITHOUT ROWID;
CREATE INDEX collection_item_sample ON collection_item(sample_id);

CREATE VIRTUAL TABLE sample_fts USING fts5(name, relpath, pack, tags, tokenize='trigram');
INSERT INTO sample_fts(sample_fts, rank) VALUES('rank', 'bm25(10.0, 2.0, 3.0, 5.0)');
`
