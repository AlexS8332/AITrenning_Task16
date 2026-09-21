package mcpserver

import (
	"context"
	"strings"

	"github.com/AlexS8332/AITrenning_Task16/internal/sources"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Инструменты внешних источников: русская Википедия и таксономическая
// база GBIF. Ключей не требуют, но требуют сети, поэтому в аннотациях у
// них OpenWorldHint: клиент вправе спросить разрешение перед вызовом.

const (
	introMaxRunes   = 1500
	sectionMaxRunes = 4000
)

// WikiSearchIn — аргументы search_wikipedia.
type WikiSearchIn struct {
	Query string `json:"query" jsonschema:"название животного на русском"`
	Limit int    `json:"limit,omitempty" jsonschema:"сколько результатов вернуть, 1..5; по умолчанию 5"`
}

// WikiHit — одна строка результатов поиска.
type WikiHit struct {
	Title   string `json:"title" jsonschema:"заголовок статьи"`
	Snippet string `json:"snippet" jsonschema:"фрагмент статьи вокруг совпадения"`
}

// WikiSearchOut — результат search_wikipedia.
type WikiSearchOut struct {
	Query   string    `json:"query" jsonschema:"запрос, по которому искали"`
	Count   int       `json:"count" jsonschema:"сколько статей нашлось"`
	Results []WikiHit `json:"results" jsonschema:"найденные статьи"`
}

// WikiReadIn — аргументы read_wikipedia.
type WikiReadIn struct {
	Title   string `json:"title" jsonschema:"точный заголовок статьи, как в результатах search_wikipedia"`
	Section string `json:"section,omitempty" jsonschema:"название раздела, например «Питание»; без него вернётся вступление и оглавление"`
}

// WikiReadOut — результат read_wikipedia.
type WikiReadOut struct {
	Title          string   `json:"title" jsonschema:"итоговый заголовок статьи после раскрытия перенаправлений"`
	URL            string   `json:"url" jsonschema:"адрес статьи"`
	RedirectedFrom string   `json:"redirected_from,omitempty" jsonschema:"исходный заголовок, если было перенаправление"`
	Intro          string   `json:"intro,omitempty" jsonschema:"вступление статьи, если раздел не запрашивался"`
	Sections       []string `json:"sections,omitempty" jsonschema:"оглавление статьи"`
	Section        string   `json:"section,omitempty" jsonschema:"название найденного раздела"`
	Found          *bool    `json:"section_found,omitempty" jsonschema:"нашёлся ли запрошенный раздел"`
	Text           string   `json:"text,omitempty" jsonschema:"текст раздела"`
	Hint           string   `json:"hint,omitempty" jsonschema:"что делать, если раздел не нашёлся"`
}

// MatchIn — аргументы match_taxon.
type MatchIn struct {
	ScientificName string `json:"scientific_name" jsonschema:"латинское (научное) название, например Lynx lynx"`
}

// TreeIn — аргументы taxon_tree. Ключ можно не знать: достаточно латыни,
// тогда сервер сам сверит её через GBIF. Лишний шаг для клиента — лишний
// повод ошибиться.
type TreeIn struct {
	UsageKey       int    `json:"usage_key,omitempty" jsonschema:"ключ таксона в GBIF из результата match_taxon"`
	ScientificName string `json:"scientific_name,omitempty" jsonschema:"латинское название вместо ключа; сервер сверит его сам"`
}

// TreeOut — результат taxon_tree.
type TreeOut struct {
	UsageKey int                 `json:"usage_key" jsonschema:"ключ таксона, по которому построено дерево"`
	Tree     []sources.TaxonNode `json:"tree" jsonschema:"цепочка от царства до самого таксона"`
}

// VernacularIn — аргументы vernacular_names.
type VernacularIn struct {
	UsageKey int    `json:"usage_key" jsonschema:"ключ таксона в GBIF"`
	Language string `json:"language,omitempty" jsonschema:"код языка ISO 639-3; по умолчанию rus"`
}

// VernacularOut — результат vernacular_names.
type VernacularOut struct {
	UsageKey int      `json:"usage_key" jsonschema:"ключ таксона"`
	Language string   `json:"language" jsonschema:"язык названий"`
	Count    int      `json:"count" jsonschema:"сколько названий нашлось"`
	Names    []string `json:"names" jsonschema:"народные названия без повторов"`
}

func (s *Server) addSourceTools() {
	online := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}

	addTool(s, &mcp.Tool{
		Name:  "search_wikipedia",
		Title: "Поиск в Википедии",
		Description: "Поиск статей в русской Википедии по названию животного. Возвращает заголовки " +
			"с фрагментами. Наличие результатов не означает, что статья о запрошенном животном есть: " +
			"сверяйте заголовок с запросом.",
		Annotations: online,
	}, s.searchWikipedia)

	addTool(s, &mcp.Tool{
		Name:  "read_wikipedia",
		Title: "Чтение статьи Википедии",
		Description: "Чтение статьи русской Википедии по точному заголовку. Без section возвращает " +
			"вступление и оглавление, с section — текст этого раздела. Так статья читается по частям, " +
			"а не целиком. Перенаправления раскрываются, итоговый заголовок в поле title.",
		Annotations: online,
	}, s.readWikipedia)

	addTool(s, &mcp.Tool{
		Name:  "match_taxon",
		Title: "Сверка латинского названия",
		Description: "Сверка латинского (научного) названия с таксономической базой GBIF. Возвращает " +
			"found, ранг, статус, уверенность и положение в системе. Нечёткое совпадение найденным " +
			"не считается: похожее название не означает то же животное.",
		Annotations: online,
	}, s.matchTaxon)

	addTool(s, &mcp.Tool{
		Name:  "taxon_tree",
		Title: "Дерево классификации",
		Description: "Дерево классификации таксона из GBIF от царства до самого таксона. " +
			"Принимает usage_key из match_taxon или латинское название — тогда сверка делается сама.",
		Annotations: online,
	}, s.taxonTree)

	addTool(s, &mcp.Tool{
		Name:  "vernacular_names",
		Title: "Народные названия",
		Description: "Народные (обиходные) названия таксона на заданном языке по данным GBIF. " +
			"По умолчанию русский (rus). Позволяет проверить, что русское название действительно " +
			"относится к этому таксону.",
		Annotations: online,
	}, s.vernacularNames)
}

func (s *Server) searchWikipedia(ctx context.Context, req *mcp.CallToolRequest, in WikiSearchIn) (*mcp.CallToolResult, WikiSearchOut, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return nil, WikiSearchOut{}, errf("query пуст")
	}
	hits, err := s.wiki.Search(ctx, query)
	if err != nil {
		return nil, WikiSearchOut{}, err
	}
	if in.Limit > 0 && in.Limit < len(hits) {
		hits = hits[:in.Limit]
	}

	out := WikiSearchOut{Query: query, Count: len(hits), Results: make([]WikiHit, 0, len(hits))}
	for _, h := range hits {
		out.Results = append(out.Results, WikiHit{Title: h.Title, Snippet: h.Snippet})
	}
	return nil, out, nil
}

func (s *Server) readWikipedia(ctx context.Context, req *mcp.CallToolRequest, in WikiReadIn) (*mcp.CallToolResult, WikiReadOut, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, WikiReadOut{}, errf("title пуст")
	}
	art, err := s.wiki.Article(ctx, title)
	if err != nil {
		return nil, WikiReadOut{}, err
	}

	out := WikiReadOut{Title: art.Title, URL: art.URL, RedirectedFrom: art.RedirectedFrom}

	section := strings.TrimSpace(in.Section)
	if section == "" {
		out.Intro = sources.Truncate(art.Intro, introMaxRunes)
		out.Sections = art.SectionTitles()
		return nil, out, nil
	}

	sec, ok := art.FindSection(section)
	out.Found = ptr(ok)
	if !ok {
		out.Section = section
		out.Sections = art.SectionTitles()
		out.Hint = "раздела с таким названием нет; выберите из списка sections или считайте, что сведений нет"
		return nil, out, nil
	}
	out.Section = sec.Title
	out.Text = sources.Truncate(sec.Text, sectionMaxRunes)
	return nil, out, nil
}

func (s *Server) matchTaxon(ctx context.Context, req *mcp.CallToolRequest, in MatchIn) (*mcp.CallToolResult, sources.Match, error) {
	name := strings.TrimSpace(in.ScientificName)
	if name == "" {
		return nil, sources.Match{}, errf("scientific_name пуст")
	}
	m, err := s.gbif.Match(ctx, name)
	if err != nil {
		return nil, sources.Match{}, err
	}
	return nil, m, nil
}

func (s *Server) taxonTree(ctx context.Context, req *mcp.CallToolRequest, in TreeIn) (*mcp.CallToolResult, TreeOut, error) {
	key := in.UsageKey
	if key <= 0 {
		name := strings.TrimSpace(in.ScientificName)
		if name == "" {
			return nil, TreeOut{}, errf("нужен usage_key или scientific_name")
		}
		m, err := s.gbif.Match(ctx, name)
		if err != nil {
			return nil, TreeOut{}, err
		}
		if !m.Found {
			return nil, TreeOut{}, errf("таксон «%s» в GBIF не подтверждён: %s", name, m.Note)
		}
		key = m.UsageKey
	}

	tree, err := s.gbif.Tree(ctx, key)
	if err != nil {
		return nil, TreeOut{}, err
	}
	return nil, TreeOut{UsageKey: key, Tree: tree}, nil
}

func (s *Server) vernacularNames(ctx context.Context, req *mcp.CallToolRequest, in VernacularIn) (*mcp.CallToolResult, VernacularOut, error) {
	if in.UsageKey <= 0 {
		return nil, VernacularOut{}, errf("usage_key должен быть положительным числом; его возвращает match_taxon")
	}
	lang := strings.ToLower(strings.TrimSpace(in.Language))
	if lang == "" {
		lang = "rus"
	}
	names, err := s.gbif.Vernacular(ctx, in.UsageKey, lang)
	if err != nil {
		return nil, VernacularOut{}, err
	}
	return nil, VernacularOut{UsageKey: in.UsageKey, Language: lang, Count: len(names), Names: names}, nil
}

func ptr[T any](v T) *T { return &v }
