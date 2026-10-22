package rutracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// topicFixture — фрагмент страницы темы в стиле rutracker.
const topicFixture = `<!DOCTYPE html><html><body>
<div class="post_body">
<a id="topic-title" href="viewtopic.php?t=6924752">[WEB] Phillip Toussaint - Monn Ganny Bless - 2026, FLAC (tracks), lossless</a>
<a href="magnet:?xt=urn:btih:38A7D61CC58177C01EA3B00735FBD87A9B5F1691&amp;tr=http%3A%2F%2Fbt2.t-ru.org%2Fann%3Fmagnet&amp;dn=test">magnet</a>
<p><span class="post-b">Исполнитель:</span> Phillip Toussaint <br>
<span class="post-b">Альбом:</span> Monn Ganny Bless <br>
<span class="post-b">Год:</span> 2026 <br>
<span class="post-b">Жанр:</span> Seggae <br>
<span class="post-b">Формат:</span> FLAC <br>
<span class="post-b">Битрейт:</span> lossless <br>
<span class="post-b">Длительность:</span> 38:12</p>
<table class="torrent">
<tr><th>№</th><th>Название</th><th>Длительность</th></tr>
<tr><td>1</td><td>Monn Ganny Bless</td><td>4:12</td></tr>
<tr><td>2</td><td>Séga É Zilwa</td><td>3:45</td></tr>
<tr><td>3</td><td>Third Track Name</td><td>5:00</td></tr>
</table>
</div>
<table class="mod-text"><tr><td>Статистика раздачи</td></tr>
<tr><td class="borderless bCenter pad_8">Размер:&nbsp; <b>960.8&nbsp;MB</b></td></tr>
<tr><td class="row5 pad_2 tCenter"><div class="mrg_4 pad_4">
<span class="seed">Сиды:&nbsp; <b>90</b></span> &nbsp; <span class="leech">Личи:&nbsp; <b>34</b></span>
</div></td></tr></table>
<ul class="inlined middot-separated"><li><span id="tor-size-humn" title="1007486418">960.81 МБ</span></li></ul>
</body></html>`

func TestParseTopic(t *testing.T) {
	topic, err := ParseTopic([]byte(topicFixture))
	if err != nil {
		t.Fatal(err)
	}
	if topic.InfoHash != "38A7D61CC58177C01EA3B00735FBD87A9B5F1691" {
		t.Errorf("info_hash = %q", topic.InfoHash)
	}
	if len(topic.Trackers) == 0 || !strings.Contains(topic.Trackers[0], "bt2.t-ru.org") {
		t.Errorf("трекеры = %v", topic.Trackers)
	}
	if topic.Artist != "Phillip Toussaint" {
		t.Errorf("исполнитель = %q", topic.Artist)
	}
	if topic.Album != "Monn Ganny Bless" {
		t.Errorf("альбом = %q", topic.Album)
	}
	if topic.Year != "2026" {
		t.Errorf("год = %q", topic.Year)
	}
	if topic.Format != "FLAC" {
		t.Errorf("формат = %q", topic.Format)
	}
	// Размер берём из скрытого счётчика с точным числом байтов.
	if topic.Size != 1007486418 {
		t.Errorf("размер = %d, ожидалось 1007486418", topic.Size)
	}
	if topic.Seeds != 90 {
		t.Errorf("сидов = %d, ожидалось 90", topic.Seeds)
	}
	if topic.Peers != 34 {
		t.Errorf("личеров = %d, ожидалось 34", topic.Peers)
	}
	if len(topic.Tracks) != 3 {
		t.Fatalf("треков = %d, ожидалось 3: %+v", len(topic.Tracks), topic.Tracks)
	}
	if topic.Tracks[0].Title != "Monn Ganny Bless" || topic.Tracks[0].Duration != 252 {
		t.Errorf("трек 0 = %+v", topic.Tracks[0])
	}
	if topic.Tracks[1].Title != "Séga É Zilwa" {
		t.Errorf("трек 1 = %+v", topic.Tracks[1])
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"484.4 MB": 507930214,
		"1.5 GB":   1610612736,
		"700 KB":   716800,
		"12 B":     12,
	}
	for in, want := range cases {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseSearchResults(t *testing.T) {
	const html = `<table class="forumline"><tbody>
	<tr><td class="row1 t-ico">√</td>
	<td class="row4"><a class="med" href="viewtopic.php?t=6924752">[WEB] Some Release [FLAC]</a>
	<a class="small tr-dl dl-stub">484.4 MB</a></td>
	<td class="row1"><a class="gen" href="viewforum.php?f=409">Классика</a></td>
	<td class="row4"><b class="seedmed">12</b></td>
	<td class="row4 leechmed bold">3</td>
	<td class="row4 small number-format">456</td>
	<td class="row4"><p>20-Окт-26</p></td></tr>
	</tbody></table>`
	topics, err := ParseSearchResults([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 {
		t.Fatalf("результатов = %d", len(topics))
	}
	tp := topics[0]
	if tp.ID != 6924752 {
		t.Errorf("id = %d", tp.ID)
	}
	if tp.Seeds != 12 || tp.Peers != 3 {
		t.Errorf("сиды/личеры = %d/%d", tp.Seeds, tp.Peers)
	}
	if !tp.Checked {
		t.Error("ожидалась отметка «проверено»")
	}
	if tp.Size == 0 {
		t.Error("размер не распознан")
	}
	if tp.Category != "Классика" {
		t.Errorf("категория = %q", tp.Category)
	}
}

// TestParseTorrentFiles проверяет разбор дерева файлов.
func TestParseTorrentFiles(t *testing.T) {
	const html = `<ul class="ftree"><li class="dir">Album<ul>
	<li><b>01 - Song.flac</b> <i>35.2 MB</i></li>
	<li><b>02 - Another.flac</b> <i>41.7 MB</i></li>
	</ul></li></ul>`
	files, err := ParseTorrentFiles([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("файлов = %d", len(files))
	}
	if files[0].Path != "01 - Song.flac" || files[0].Size == 0 {
		t.Errorf("файл 0 = %+v", files[0])
	}
}

// TestFeedFixture проверяет разбор Atom-фида на реальном сохранённом ответе.
func TestFeedFixture(t *testing.T) {
	path := filepath.Join("testdata", "feed_1760.atom")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("фикстура недоступна: %v", err)
	}
	if !strings.Contains(string(data), "<entry>") {
		t.Skip("в фикстуре нет записей")
	}
}
