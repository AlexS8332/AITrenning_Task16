package sources

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	DefaultWikipediaBase = "https://ru.wikipedia.org"

	searchLimit = 5
)

// Wikipedia — поиск и чтение статей русской Википедии.
type Wikipedia struct {
	Base string
	f    *Fetcher
}

func NewWikipedia(base string, f *Fetcher) *Wikipedia {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultWikipediaBase
	}
	return &Wikipedia{Base: base, f: f}
}

// SearchHit — одна строка результатов поиска.
type SearchHit struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Search ищет статьи по запросу.
func (w *Wikipedia) Search(ctx context.Context, query string) ([]SearchHit, error) {
	q := url.Values{}
	q.Set("action", "query")
	q.Set("list", "search")
	q.Set("srsearch", query)
	q.Set("srlimit", fmt.Sprint(searchLimit))
	q.Set("format", "json")
	q.Set("formatversion", "2")

	var resp struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := w.f.GetJSON(ctx, w.Base+"/w/api.php?"+q.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("поиск в Википедии: %w", err)
	}

	hits := make([]SearchHit, 0, len(resp.Query.Search))
	for _, s := range resp.Query.Search {
		hits = append(hits, SearchHit{Title: s.Title, Snippet: stripHTML(s.Snippet)})
	}
	return hits, nil
}

// Article — статья в разобранном виде: вступление и разделы.
type Article struct {
	Title          string
	URL            string
	RedirectedFrom string
	Intro          string
	Sections       []Section
}

// Section — раздел статьи. Level 2 — «== Заголовок ==», 3 — «=== … ===».
// Text раздела второго уровня включает его подразделы: «Питание» часто
// лежит внутри «Образ жизни» третьим уровнем, и брать его нужно вместе.
type Section struct {
	Title string
	Level int
	Text  string
}

// SectionTitles — оглавление для модели. Подразделы помечены отступом:
// видно, что «Подвиды» лежат внутри «Распространения».
func (a *Article) SectionTitles() []string {
	titles := make([]string, 0, len(a.Sections))
	for _, s := range a.Sections {
		if s.Level > 2 {
			titles = append(titles, "  "+s.Title)
			continue
		}
		titles = append(titles, s.Title)
	}
	return titles
}

// FindSection ищет раздел по подстроке названия без учёта регистра.
// Точное совпадение предпочтительнее частичного: «Питание» должно найти
// «Питание», а не «Образ жизни, поведение и питание», если есть оба.
func (a *Article) FindSection(name string) (Section, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return Section{}, false
	}
	for _, s := range a.Sections {
		if strings.ToLower(s.Title) == want {
			return s, true
		}
	}
	for _, s := range a.Sections {
		if strings.Contains(strings.ToLower(s.Title), want) {
			return s, true
		}
	}
	return Section{}, false
}

// Article загружает статью текстом и разбирает её на разделы.
func (w *Wikipedia) Article(ctx context.Context, title string) (*Article, error) {
	q := url.Values{}
	q.Set("action", "query")
	q.Set("prop", "extracts")
	q.Set("explaintext", "1")
	q.Set("redirects", "1")
	q.Set("titles", title)
	q.Set("format", "json")
	q.Set("formatversion", "2")

	var resp struct {
		Query struct {
			Redirects []struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"redirects"`
			Pages []struct {
				Title   string `json:"title"`
				Missing bool   `json:"missing"`
				Extract string `json:"extract"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := w.f.GetJSON(ctx, w.Base+"/w/api.php?"+q.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("чтение статьи: %w", err)
	}
	if len(resp.Query.Pages) == 0 || resp.Query.Pages[0].Missing {
		return nil, fmt.Errorf("статьи «%s» в Википедии нет", title)
	}

	page := resp.Query.Pages[0]
	art := &Article{
		Title: page.Title,
		URL:   w.Base + "/wiki/" + url.PathEscape(strings.ReplaceAll(page.Title, " ", "_")),
	}
	for _, r := range resp.Query.Redirects {
		if r.To == page.Title {
			art.RedirectedFrom = r.From
		}
	}
	art.Intro, art.Sections = splitSections(page.Extract)
	return art, nil
}

var headingRe = regexp.MustCompile(`^(={2,})\s*(.+?)\s*=+\s*$`)

// splitSections делит текст статьи по заголовкам «== … ==». Разделы идут
// в порядке появления; текст раздела второго уровня включает тексты его
// подразделов, а подразделы остаются и отдельными записями.
func splitSections(extract string) (intro string, sections []Section) {
	var (
		introLines []string
		bodies     []strings.Builder
	)

	for _, line := range strings.Split(extract, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			sections = append(sections, Section{Title: strings.TrimSpace(m[2]), Level: len(m[1])})
			bodies = append(bodies, strings.Builder{})
			continue
		}
		if len(sections) == 0 {
			introLines = append(introLines, line)
			continue
		}
		b := &bodies[len(bodies)-1]
		b.WriteString(line)
		b.WriteByte('\n')
	}

	for i := range sections {
		var text strings.Builder
		text.WriteString(bodies[i].String())
		if sections[i].Level == 2 {
			// Подразделы принадлежат родителю до следующего заголовка второго уровня.
			for j := i + 1; j < len(sections) && sections[j].Level > 2; j++ {
				text.WriteByte('\n')
				text.WriteString(sections[j].Title)
				text.WriteByte('\n')
				text.WriteString(bodies[j].String())
			}
		}
		sections[i].Text = strings.TrimSpace(text.String())
	}
	return strings.TrimSpace(strings.Join(introLines, "\n")), sections
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.TrimSpace(s)
}
