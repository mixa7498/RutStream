package mediaindex

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// buildTestInfo создаёт мультифайловый metainfo с заданными файлами.
func buildTestInfo(name string, pieceLen int64, sizes []int64) *metainfo.Info {
	info := &metainfo.Info{
		Name:        name,
		PieceLength: pieceLen,
	}
	for i, sz := range sizes {
		info.Files = append(info.Files, metainfo.FileInfo{
			Length: sz,
			Path:   []string{name, string(rune('A'+i)) + ".flac"},
		})
	}
	return info
}

func TestPieceRangeFor(t *testing.T) {
	cases := []struct {
		start, end, pieceLen int64
		wantStart, wantEnd   int
	}{
		{0, 1, 256, 0, 1},
		{0, 256, 256, 0, 1},
		{0, 257, 256, 0, 2},
		{256, 512, 256, 1, 2},
		{255, 257, 256, 0, 2},
		{1000, 2000, 256, 3, 8},
	}
	for _, c := range cases {
		got := PieceRangeFor(c.start, c.end, c.pieceLen)
		if got.Start != c.wantStart || got.End != c.wantEnd {
			t.Errorf("PieceRangeFor(%d,%d,%d) = %+v, want {%d %d}", c.start, c.end, c.pieceLen, got, c.wantStart, c.wantEnd)
		}
	}
}

func TestExtraBytes(t *testing.T) {
	// файл ровно в один piece — лишних байтов нет
	if got := ExtraBytes(0, 256, 256); got != 0 {
		t.Errorf("ExtraBytes aligned = %d, want 0", got)
	}
	// файл со смещением 100, длина 100 => piece 0 целиком: 256-100 + 100-100 = 156
	if got := ExtraBytes(100, 200, 256); got != 156 {
		t.Errorf("ExtraBytes(100,200,256) = %d, want 156", got)
	}
}

func TestPlanForFileOffsets(t *testing.T) {
	info := buildTestInfo("Disc", 256, []int64{1000, 2000, 3000})
	files := FilesInfo(info)
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3", len(files))
	}
	// Проверяем, что смещения склеены правильно
	wantOffsets := []int64{0, 1000, 3000}
	for i, f := range files {
		if f.TorrentOffset != wantOffsets[i] {
			t.Errorf("file %d offset = %d, want %d", i, f.TorrentOffset, wantOffsets[i])
		}
	}
	plan, err := PlanForFile(info, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ByteStart != 1000 || plan.ByteEnd != 3000 {
		t.Errorf("plan bytes = [%d,%d), want [1000,3000)", plan.ByteStart, plan.ByteEnd)
	}
	if plan.PieceStart != 3 || plan.PieceEnd != 12 {
		t.Errorf("plan pieces = [%d,%d), want [3,12)", plan.PieceStart, plan.PieceEnd)
	}
}

func TestEstimateDownload(t *testing.T) {
	// Раздача 500 МБ, трек 5 МБ => качаем ~5 МБ, а не 500 МБ
	const mb = 1 << 20
	sizes := make([]int64, 100)
	for i := range sizes {
		sizes[i] = 5 * mb
	}
	info := buildTestInfo("Discography", 256*1024, sizes)
	bytes, pieces, extra, err := EstimateDownload(info, 50)
	if err != nil {
		t.Fatal(err)
	}
	if bytes > 6*mb {
		t.Errorf("оценка загрузки %d байт, ожидалось ≤ 6 МБ", bytes)
	}
	if extra > 512*1024 {
		t.Errorf("лишних байтов %d, ожидалось ≤ 512 КБ", extra)
	}
	if pieces <= 0 {
		t.Error("pieces должен быть > 0")
	}
	t.Logf("трек 5 МБ из раздачи 500 МБ: скачаем %d байт (%d piece'ов), лишних %d байт", bytes, pieces, extra)
}

func TestMergeRangesDoesNotSpanGaps(t *testing.T) {
	// Два файла с промежутком: диапазоны НЕ должны сливаться в один большой.
	ranges := []PieceRange{{Start: 0, End: 2}, {Start: 10, End: 12}}
	merged := MergeRanges(ranges)
	if len(merged) != 2 {
		t.Fatalf("merged = %+v, want 2 диапазона", merged)
	}
	// Соседние диапазоны должны слиться
	merged = MergeRanges([]PieceRange{{Start: 0, End: 2}, {Start: 2, End: 5}})
	if len(merged) != 1 || merged[0].End != 5 {
		t.Errorf("merged = %+v, want [{0 5}]", merged)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"01 - Song Name.flac", "song name"},
		{"03. Another Track.mp3", "another track"},
		{"[07] Bracketed Name.flac", "bracketed name"},
		{"Track (feat. Someone).flac", "track feat someone"},
		{"Ёлка", "елка"},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScore(t *testing.T) {
	cases := []struct {
		a, b string
		min  float64
	}{
		{"Creep", "creep", 1.0},
		{"01 - Creep.flac", "Creep", 0.99},
		{"Karma Police", "Karma Polices", 0.8},
		{"Creep", "Totally Different Song", 0.0},
	}
	for _, c := range cases {
		got := Score(c.a, c.b)
		if got < c.min {
			t.Errorf("Score(%q,%q) = %.2f, want ≥ %.2f", c.a, c.b, got, c.min)
		}
	}
}

func TestTrackNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"01 - Song.flac", 1, true},
		{"12. Song.flac", 12, true},
		{"[03] Song.flac", 3, true},
		{"Song.flac", 0, false},
	}
	for _, c := range cases {
		n, ok := TrackNumber(c.in)
		if n != c.want || ok != c.ok {
			t.Errorf("TrackNumber(%q) = (%d,%v), want (%d,%v)", c.in, n, ok, c.want, c.ok)
		}
	}
}
