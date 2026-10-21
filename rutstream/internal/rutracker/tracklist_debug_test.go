package rutracker

import (
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// TestTracklistParsing проверяет разбор реальной страницы темы.
func TestTracklistParsing(t *testing.T) {
	path := os.Getenv("TEMP") + `\rt_topic.html`
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("нет фикстуры: %v", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	// Что вообще есть на странице?
	t.Logf("div.sp-wrap: %d, div.sp-head: %d, div.sp-body: %d",
		doc.Find("div.sp-wrap").Length(), doc.Find("div.sp-head").Length(), doc.Find("div.sp-body").Length())
	doc.Find("div.sp-head, div.sp-wrap, span.post-b").EachWithBreak(func(i int, s *goquery.Selection) bool {
		txt := strings.TrimSpace(s.Text())
		if i < 12 {
			t.Logf("  [%d] %s: %q", i, goquery.NodeName(s), truncate(txt, 40))
		}
		return true
	})
	blk := findTracklistBlock(doc)
	if blk == nil {
		t.Fatal("блок треклиста не найден")
	}
	html, _ := blk.Html()
	lines := strings.Split(html, "<br")
	t.Logf("строк в блоке: %d", len(lines))
	for i, l := range lines {
		if i > 8 {
			break
		}
		t.Logf("  [%d] %q", i, truncate(stripTags(l), 70))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
