// Package catalog — локальное хранилище каталога: разделы, раздачи, треки,
// индекс треков внутри торрентов, история прослушиваний.
package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store — хранилище каталога.
type Store struct {
	db *sql.DB
}

// Open открывает (или создаёт) базу каталога.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	st := &Store{db: db}
	if err := st.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

// Close закрывает базу.
func (s *Store) Close() error { return s.db.Close() }

// DB возвращает underlying-соединение (для сложных запросов).
func (s *Store) DB() *sql.DB { return s.db }

const schema = `
CREATE TABLE IF NOT EXISTS forum (
    id         INTEGER PRIMARY KEY,
    cat_id     INTEGER NOT NULL DEFAULT 0,
    cat_title  TEXT    NOT NULL DEFAULT '',
    title      TEXT    NOT NULL,
    url        TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS topic (
    id         INTEGER PRIMARY KEY,
    forum_id   INTEGER NOT NULL DEFAULT 0,
    title      TEXT    NOT NULL DEFAULT '',
    norm_title TEXT    NOT NULL DEFAULT '',
    infohash   TEXT    NOT NULL DEFAULT '',
    size       INTEGER NOT NULL DEFAULT 0,
    seeds      INTEGER NOT NULL DEFAULT 0,
    peers      INTEGER NOT NULL DEFAULT 0,
    rating     REAL    NOT NULL DEFAULT 0,
    downloads  INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    fetched_at INTEGER NOT NULL DEFAULT 0,
    artist     TEXT    NOT NULL DEFAULT '',
    album      TEXT    NOT NULL DEFAULT '',
    year       TEXT    NOT NULL DEFAULT '',
    genre      TEXT    NOT NULL DEFAULT '',
    format     TEXT    NOT NULL DEFAULT '',
    bitrate    TEXT    NOT NULL DEFAULT '',
    lossless   INTEGER NOT NULL DEFAULT 0,
    tracks     INTEGER NOT NULL DEFAULT 0,
    raw        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS topic_forum ON topic(forum_id, seeds DESC);
CREATE INDEX IF NOT EXISTS topic_hash   ON topic(infohash);
CREATE INDEX IF NOT EXISTS topic_title  ON topic(norm_title);

CREATE TABLE IF NOT EXISTS torrent (
    infohash     TEXT PRIMARY KEY,
    topic_id     INTEGER NOT NULL DEFAULT 0,
    name         TEXT    NOT NULL DEFAULT '',
    piece_length INTEGER NOT NULL DEFAULT 0,
    total_length INTEGER NOT NULL DEFAULT 0,
    num_pieces   INTEGER NOT NULL DEFAULT 0,
    announce     TEXT    NOT NULL DEFAULT '',
    added_at     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS torrent_file (
    infohash TEXT    NOT NULL,
    idx      INTEGER NOT NULL,
    path     TEXT    NOT NULL,
    offset   INTEGER NOT NULL DEFAULT 0,
    length   INTEGER NOT NULL DEFAULT 0,
    is_audio INTEGER NOT NULL DEFAULT 0,
    codec    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (infohash, idx)
);

CREATE TABLE IF NOT EXISTS track_index (
    infohash   TEXT    NOT NULL,
    track_no   INTEGER NOT NULL,
    file_idx   INTEGER NOT NULL,
    byte_start INTEGER NOT NULL DEFAULT 0,
    byte_end   INTEGER NOT NULL DEFAULT 0,
    piece_start INTEGER NOT NULL DEFAULT 0,
    piece_end  INTEGER NOT NULL DEFAULT 0,
    codec      TEXT    NOT NULL DEFAULT '',
    lossless   INTEGER NOT NULL DEFAULT 0,
    duration   REAL    NOT NULL DEFAULT 0,
    title      TEXT    NOT NULL DEFAULT '',
    source     TEXT    NOT NULL DEFAULT '',
    confidence REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY (infohash, track_no)
);

CREATE TABLE IF NOT EXISTS track (
    id         TEXT PRIMARY KEY,
    topic_id   INTEGER NOT NULL DEFAULT 0,
    infohash   TEXT    NOT NULL DEFAULT '',
    artist     TEXT    NOT NULL DEFAULT '',
    album      TEXT    NOT NULL DEFAULT '',
    title      TEXT    NOT NULL DEFAULT '',
    norm_title TEXT    NOT NULL DEFAULT '',
    disc       INTEGER NOT NULL DEFAULT 0,
    pos        INTEGER NOT NULL DEFAULT 0,
    duration   REAL    NOT NULL DEFAULT 0,
    format     TEXT    NOT NULL DEFAULT '',
    lossless   INTEGER NOT NULL DEFAULT 0,
    bitrate    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS track_norm ON track(norm_title);
CREATE INDEX IF NOT EXISTS track_artist ON track(artist);

CREATE TABLE IF NOT EXISTS history (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id  TEXT    NOT NULL,
    played_at INTEGER NOT NULL,
    ms_played INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS history_track ON history(track_id, played_at DESC);

CREATE TABLE IF NOT EXISTS kv (
    k          TEXT PRIMARY KEY,
    v          TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT 0
);
`

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// ---------------------------------------------------------------- разделы --

// Forum — раздел форума.
type Forum struct {
	ID       int64  `json:"id"`
	CatID    int64  `json:"catId"`
	CatTitle string `json:"catTitle"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// SaveForums сохраняет список разделов (заменяет содержимое таблицы).
func (s *Store) SaveForums(ctx context.Context, forums []Forum) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM forum"); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO forum
        (id, cat_id, cat_title, title, url, updated_at) VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().Unix()
	for _, f := range forums {
		if _, err := stmt.ExecContext(ctx, f.ID, f.CatID, f.CatTitle, f.Title, f.URL, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Forums возвращает разделы, отсортированные по названию.
func (s *Store) Forums(ctx context.Context) ([]Forum, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, cat_id, cat_title, title, url FROM forum ORDER BY cat_title, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Forum
	for rows.Next() {
		var f Forum
		if err := rows.Scan(&f.ID, &f.CatID, &f.CatTitle, &f.Title, &f.URL); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- раздачи --

// Topic — раздача в каталоге.
type Topic struct {
	ID        int64  `json:"id"`
	ForumID   int64  `json:"forumId"`
	Title     string `json:"title"`
	InfoHash  string `json:"infoHash"`
	Size      int64  `json:"size"`
	Seeds     int    `json:"seeds"`
	Peers     int    `json:"peers"`
	Rating    float64 `json:"rating"`
	Downloads int    `json:"downloads"`
	CreatedAt int64  `json:"createdAt"`
	FetchedAt int64  `json:"fetchedAt"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Year      string `json:"year"`
	Genre     string `json:"genre"`
	Format    string `json:"format"`
	Bitrate   string `json:"bitrate"`
	Lossless  bool   `json:"lossless"`
	Tracks    int    `json:"tracks"`
}

// SaveTopic сохраняет (или обновляет) раздачу.
func (s *Store) SaveTopic(ctx context.Context, t Topic) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO topic
        (id, forum_id, title, norm_title, infohash, size, seeds, peers, rating, downloads,
         created_at, fetched_at, artist, album, year, genre, format, bitrate, lossless, tracks)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET
            forum_id=excluded.forum_id, title=excluded.title, norm_title=excluded.norm_title,
            infohash=excluded.infohash, size=excluded.size, seeds=excluded.seeds, peers=excluded.peers,
            rating=excluded.rating, downloads=excluded.downloads, fetched_at=excluded.fetched_at,
            artist=excluded.artist, album=excluded.album, year=excluded.year, genre=excluded.genre,
            format=excluded.format, bitrate=excluded.bitrate, lossless=excluded.lossless,
            tracks=excluded.tracks`,
		t.ID, t.ForumID, t.Title, Normalize(t.Title), t.InfoHash, t.Size, t.Seeds, t.Peers,
		t.Rating, t.Downloads, t.CreatedAt, time.Now().Unix(), t.Artist, t.Album, t.Year,
		t.Genre, t.Format, t.Bitrate, boolToInt(t.Lossless), t.Tracks)
	return err
}

// TopicsByForum возвращает раздачи раздела.
func (s *Store) TopicsByForum(ctx context.Context, forumID int64, limit int) ([]Topic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, forum_id, title, infohash, size, seeds, peers,
            rating, downloads, created_at, fetched_at, artist, album, year, genre, format, bitrate,
            lossless, tracks
        FROM topic WHERE forum_id = ? ORDER BY seeds DESC, fetched_at DESC LIMIT ?`, forumID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTopics(rows)
}

// SearchTopics ищет раздачи по подстроке названия.
func (s *Store) SearchTopics(ctx context.Context, query string, limit int) ([]Topic, error) {
	q := "%" + Normalize(query) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id, forum_id, title, infohash, size, seeds, peers,
            rating, downloads, created_at, fetched_at, artist, album, year, genre, format, bitrate,
            lossless, tracks
        FROM topic WHERE norm_title LIKE ? OR artist LIKE ? OR album LIKE ?
        ORDER BY seeds DESC LIMIT ?`, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTopics(rows)
}

func scanTopics(rows *sql.Rows) ([]Topic, error) {
	var out []Topic
	for rows.Next() {
		var t Topic
		var lossless int
		if err := rows.Scan(&t.ID, &t.ForumID, &t.Title, &t.InfoHash, &t.Size, &t.Seeds, &t.Peers,
			&t.Rating, &t.Downloads, &t.CreatedAt, &t.FetchedAt, &t.Artist, &t.Album, &t.Year,
			&t.Genre, &t.Format, &t.Bitrate, &lossless, &t.Tracks); err != nil {
			return nil, err
		}
		t.Lossless = lossless != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- треки --

// Track — трек в каталоге.
type Track struct {
	ID        string  `json:"id"`
	TopicID   int64   `json:"topicId"`
	InfoHash  string  `json:"infoHash"`
	Artist    string  `json:"artist"`
	Album     string  `json:"album"`
	Title     string  `json:"title"`
	Disc      int     `json:"disc"`
	Pos       int     `json:"pos"`
	Duration  float64 `json:"duration"`
	Format    string  `json:"format"`
	Lossless  bool    `json:"lossless"`
	Bitrate   int     `json:"bitrate"`
}

// SaveTracks сохраняет треки раздачи (заменяет список для этой раздачи).
func (s *Store) SaveTracks(ctx context.Context, topicID int64, tracks []Track) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM track WHERE topic_id = ?", topicID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO track
        (id, topic_id, infohash, artist, album, title, norm_title, disc, pos, duration, format, lossless, bitrate)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, t := range tracks {
		if _, err := stmt.ExecContext(ctx, t.ID, topicID, t.InfoHash, t.Artist, t.Album, t.Title,
			Normalize(t.Title), t.Disc, t.Pos, t.Duration, t.Format, boolToInt(t.Lossless), t.Bitrate); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TracksByTopic возвращает треки раздачи.
func (s *Store) TracksByTopic(ctx context.Context, topicID int64) ([]Track, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, topic_id, infohash, artist, album, title, disc, pos,
            duration, format, lossless, bitrate FROM track WHERE topic_id = ? ORDER BY disc, pos`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		var t Track
		var lossless int
		if err := rows.Scan(&t.ID, &t.TopicID, &t.InfoHash, &t.Artist, &t.Album, &t.Title,
			&t.Disc, &t.Pos, &t.Duration, &t.Format, &lossless, &t.Bitrate); err != nil {
			return nil, err
		}
		t.Lossless = lossless != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// SearchTracks ищет треки по названию/исполнителю/альбому.
func (s *Store) SearchTracks(ctx context.Context, query string, limit int) ([]Track, error) {
	q := "%" + Normalize(query) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id, topic_id, infohash, artist, album, title, disc, pos,
            duration, format, lossless, bitrate FROM track
        WHERE norm_title LIKE ? OR lower(artist) LIKE ? OR lower(album) LIKE ?
        ORDER BY lossless DESC, duration DESC LIMIT ?`, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		var t Track
		var lossless int
		if err := rows.Scan(&t.ID, &t.TopicID, &t.InfoHash, &t.Artist, &t.Album, &t.Title,
			&t.Disc, &t.Pos, &t.Duration, &t.Format, &lossless, &t.Bitrate); err != nil {
			return nil, err
		}
		t.Lossless = lossless != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// ------------------------------------------------------- индекс треков ----

// TrackIndexEntry — запись индекса «трек → файл → piece-диапазон».
type TrackIndexEntry struct {
	InfoHash   string
	TrackNo    int
	FileIdx    int
	ByteStart  int64
	ByteEnd    int64
	PieceStart int
	PieceEnd   int
	Codec      string
	Lossless   bool
	Duration   float64
	Title      string
	Source     string
	Confidence float64
}

// SaveTrackIndex сохраняет индекс треков раздачи.
func (s *Store) SaveTrackIndex(ctx context.Context, entries []TrackIndexEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(entries) > 0 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM track_index WHERE infohash = ?", entries[0].InfoHash); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO track_index
        (infohash, track_no, file_idx, byte_start, byte_end, piece_start, piece_end, codec, lossless,
         duration, title, source, confidence)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, e.InfoHash, e.TrackNo, e.FileIdx, e.ByteStart, e.ByteEnd,
			e.PieceStart, e.PieceEnd, e.Codec, boolToInt(e.Lossless), e.Duration, e.Title, e.Source,
			e.Confidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TrackIndex возвращает индекс треков раздачи.
func (s *Store) TrackIndex(ctx context.Context, infohash string) ([]TrackIndexEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT infohash, track_no, file_idx, byte_start, byte_end,
        piece_start, piece_end, codec, lossless, duration, title, source, confidence
        FROM track_index WHERE infohash = ? ORDER BY track_no`, infohash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackIndexEntry
	for rows.Next() {
		var e TrackIndexEntry
		var lossless int
		if err := rows.Scan(&e.InfoHash, &e.TrackNo, &e.FileIdx, &e.ByteStart, &e.ByteEnd,
			&e.PieceStart, &e.PieceEnd, &e.Codec, &lossless, &e.Duration, &e.Title, &e.Source,
			&e.Confidence); err != nil {
			return nil, err
		}
		e.Lossless = lossless != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// --------------------------------------------------------------- история --

// AddHistory записывает факт прослушивания.
func (s *Store) AddHistory(ctx context.Context, trackID string, msPlayed int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO history (track_id, played_at, ms_played) VALUES (?,?,?)",
		trackID, time.Now().Unix(), msPlayed)
	return err
}

// History возвращает последние прослушивания.
func (s *Store) History(ctx context.Context, limit int) ([]HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.track_id, h.played_at, h.ms_played, t.title, t.artist
        FROM history h LEFT JOIN track t ON t.id = h.track_id
        ORDER BY h.played_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.TrackID, &e.PlayedAt, &e.MsPlayed, &e.Title, &e.Artist); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HistoryEntry — запись истории.
type HistoryEntry struct {
	TrackID  string `json:"trackId"`
	PlayedAt int64  `json:"playedAt"`
	MsPlayed int64  `json:"msPlayed"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
}

// ------------------------------------------------------------------ kv ----

// SetKV сохраняет значение в служебной таблице.
func (s *Store) SetKV(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv (k, v, updated_at) VALUES (?,?,?)
        ON CONFLICT(k) DO UPDATE SET v=excluded.v, updated_at=excluded.updated_at`,
		k, v, time.Now().Unix())
	return err
}

// GetKV читает значение из служебной таблицы.
func (s *Store) GetKV(ctx context.Context, k string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT v FROM kv WHERE k = ?", k).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// Normalize приводит строку к каноническому виду для поиска.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
