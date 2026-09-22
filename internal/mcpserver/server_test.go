package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AlexS8332/AITrenning_Task16/internal/sources"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Тесты поднимают настоящее MCP-соединение, только без процессов и
// труб: транспорт в памяти соединяет клиента и сервер напрямую. Сеть не
// нужна — Википедия и GBIF подменяются httptest-серверами.

// connect поднимает сервер и подключает к нему клиента.
func connect(t *testing.T, o Options) *mcp.ClientSession {
	t.Helper()

	ctx := t.Context()
	if o.Store == nil && o.DBPath == "" {
		// У каждого теста своя база во временном файле: тесты не должны
		// видеть правок друг друга, а запись здесь проверяется всерьёз.
		o.DBPath = filepath.Join(t.TempDir(), "animals.db")
	}

	srv, err := New(ctx, o)
	if err != nil {
		t.Fatalf("сервер не собрался: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("сервер не принял соединение: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("клиент не подключился: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// call вызывает инструмент и разбирает результат в target.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, target any) *mcp.CallToolResult {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: вызов не прошёл: %v", name, err)
	}
	if target == nil {
		return res
	}
	if res.IsError {
		t.Fatalf("%s: инструмент вернул ошибку: %s", name, text(res))
	}
	if err := json.Unmarshal([]byte(text(res)), target); err != nil {
		t.Fatalf("%s: результат не разобрался: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestInitializeAndListTools(t *testing.T) {
	session := connect(t, Options{Version: "test"})

	init := session.InitializeResult()
	if init.ServerInfo.Name != Name {
		t.Errorf("имя сервера %q", init.ServerInfo.Name)
	}
	if init.ServerInfo.Version != "test" {
		t.Errorf("версия сервера %q", init.ServerInfo.Version)
	}
	if init.Capabilities.Tools == nil {
		t.Fatal("сервер не объявил возможность tools")
	}
	if init.Instructions == "" {
		t.Error("сервер не прислал инструкцию")
	}

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("список инструментов не получен: %v", err)
	}

	want := []string{
		"add_animal", "compare_animals", "delete_animal", "get_animal",
		"list_animals", "match_taxon", "random_animal", "read_wikipedia",
		"search_animals", "search_wikipedia", "server_info", "taxon_tree",
		"update_animal", "vernacular_names",
	}
	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)

		if tool.Description == "" {
			t.Errorf("%s: нет описания", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("%s: нет схемы аргументов", tool.Name)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("инструменты:\n получили %v\n ожидали  %v", got, want)
	}
}

// Схема аргументов — часть ответа tools/list, по которой клиент строит
// вызов. Если она перестанет объявлять обязательные поля, ошибётся не
// сервер, а тот, кто ему доверился.
func TestInputSchemaDeclaresRequiredArgs(t *testing.T) {
	session := connect(t, Options{})

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("список инструментов не получен: %v", err)
	}

	want := map[string][]string{
		"get_animal":       {"animal"},
		"compare_animals":  {"animals"},
		"search_wikipedia": {"query"},
		"read_wikipedia":   {"title"},
		"match_taxon":      {"scientific_name"},
		"vernacular_names": {"usage_key"},
		"server_info":      nil,
		"random_animal":    nil,
		"add_animal":       {"class", "id", "name", "scientific_name"},
		"update_animal":    {"animal"},
		"delete_animal":    {"animal"},
	}

	for _, tool := range res.Tools {
		expected, ok := want[tool.Name]
		if !ok {
			continue
		}

		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: схема не сериализуется: %v", tool.Name, err)
		}
		var schema struct {
			Type     string `json:"type"`
			Required []string
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: схема не разобралась: %v", tool.Name, err)
		}
		if schema.Type != "object" {
			t.Errorf("%s: тип схемы %q, а спецификация требует object", tool.Name, schema.Type)
		}
		slices.Sort(schema.Required)
		if !slices.Equal(schema.Required, expected) {
			t.Errorf("%s: обязательные аргументы %v, ожидались %v", tool.Name, schema.Required, expected)
		}
	}
}

func TestCatalogTools(t *testing.T) {
	session := connect(t, Options{})

	t.Run("list", func(t *testing.T) {
		var out ListOut
		call(t, session, "list_animals", map[string]any{"class": "птицы"}, &out)
		if out.Total == 0 || out.Count != out.Total {
			t.Fatalf("получили total=%d count=%d", out.Total, out.Count)
		}
		for _, a := range out.Animals {
			if a.Class != "птицы" {
				t.Errorf("%s: класс %q", a.ID, a.Class)
			}
		}
	})

	t.Run("постраничный список", func(t *testing.T) {
		var first, second ListOut
		call(t, session, "list_animals", map[string]any{"limit": 3}, &first)
		call(t, session, "list_animals", map[string]any{"limit": 3, "offset": 3}, &second)

		if first.Count != 3 || second.Count != 3 {
			t.Fatalf("страницы по %d и %d записей", first.Count, second.Count)
		}
		if first.Animals[0].ID == second.Animals[0].ID {
			t.Error("вторая страница повторяет первую")
		}
	})

	t.Run("карточка", func(t *testing.T) {
		var out GetOut
		call(t, session, "get_animal", map[string]any{"animal": "Lynx lynx"}, &out)
		if !out.Found || out.Animal == nil || out.Animal.ID != "lynx" {
			t.Fatalf("получили %+v", out)
		}
	})

	t.Run("карточки нет — это не ошибка", func(t *testing.T) {
		var out GetOut
		res := call(t, session, "get_animal", map[string]any{"animal": "малая выхухоль"}, &out)
		if res.IsError {
			t.Fatal("отсутствие записи пришло как ошибка инструмента")
		}
		if out.Found || out.Hint == "" {
			t.Fatalf("получили %+v", out)
		}
	})

	t.Run("поиск по весу", func(t *testing.T) {
		var out SearchOut
		call(t, session, "search_animals", map[string]any{"diet": "хищник", "min_weight_kg": 100}, &out)
		if out.Count == 0 {
			t.Fatal("крупных хищников не нашлось")
		}
	})

	t.Run("сравнение", func(t *testing.T) {
		var out CompareOut
		call(t, session, "compare_animals", map[string]any{
			"animals": []any{"lynx", "amur-tiger"},
			"fields":  []any{"family", "diet", "weight_kg"},
		}, &out)

		if len(out.Columns) != 2 || len(out.Rows) != 3 {
			t.Fatalf("получили %d колонок и %d строк", len(out.Columns), len(out.Rows))
		}
		for _, row := range out.Rows {
			switch row.Field {
			case "family", "diet":
				if !row.Same {
					t.Errorf("%s: у рыси и тигра значения разошлись: %v", row.Field, row.Values)
				}
			case "weight_kg":
				if row.Same {
					t.Errorf("вес рыси и тигра совпал: %v", row.Values)
				}
			}
		}
	})

	t.Run("случайное животное", func(t *testing.T) {
		var out RandomOut
		call(t, session, "random_animal", nil, &out)
		if out.Animal.ID == "" {
			t.Fatal("случайная запись пуста")
		}
	})
}

// Ошибка инструмента (в отличие от ошибки протокола) приезжает обычным
// результатом с поднятым флагом: сервер остаётся жив, а клиент получает
// текст, по которому может исправиться.
func TestToolErrorsAreResultsNotProtocolFailures(t *testing.T) {
	session := connect(t, Options{})

	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"мало животных", "compare_animals", map[string]any{"animals": []any{"lynx"}}, "от двух до четырёх"},
		{"неизвестное животное", "compare_animals", map[string]any{"animals": []any{"lynx", "дракон"}}, "нет в справочнике"},
		{"неизвестное поле", "compare_animals", map[string]any{"animals": []any{"lynx", "grey-wolf"}, "fields": []any{"цвет"}}, "доступны"},
		{"пустое название", "get_animal", map[string]any{"animal": "  "}, "пуст"},
		{"нет ключа таксона", "taxon_tree", map[string]any{}, "usage_key"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := call(t, session, c.tool, c.args, nil)
			if !res.IsError {
				t.Fatalf("ожидалась ошибка инструмента, получили %s", text(res))
			}
			if !strings.Contains(text(res), c.want) {
				t.Errorf("в тексте ошибки нет %q: %s", c.want, text(res))
			}
		})
	}
}

// Аргументы проверяются по схеме до обработчика: инструмент до чужого
// типа не доходит. Отказ приезжает результатом с IsError, а не сбоем
// протокола — клиент видит, какое поле и чем не устроило сервер.
func TestInvalidArgumentsRejectedBySchema(t *testing.T) {
	session := connect(t, Options{})

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "list_animals",
		Arguments: map[string]any{"limit": "двадцать"},
	})
	if err != nil {
		t.Fatalf("вызов не прошёл: %v", err)
	}
	if !res.IsError {
		t.Fatal("сервер принял limit строкой")
	}
	if !strings.Contains(text(res), "/properties/limit") {
		t.Errorf("в отказе не названо поле: %s", text(res))
	}

	// А вот несуществующий инструмент — уже ошибка протокола: такого
	// метода у сервера нет, и отвечать на него нечем.
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "нет_такого"}); err == nil {
		t.Fatal("сервер принял вызов несуществующего инструмента")
	}
}

func TestServerInfoCountsCalls(t *testing.T) {
	session := connect(t, Options{Version: "1.2.3"})

	call(t, session, "random_animal", nil, nil)
	call(t, session, "random_animal", nil, nil)

	var info ServerInfo
	call(t, session, "server_info", nil, &info)

	if info.Version != "1.2.3" || info.Server != Name {
		t.Errorf("получили %s %s", info.Server, info.Version)
	}
	if len(info.Tools) != 14 {
		t.Errorf("инструментов %d: %v", len(info.Tools), info.Tools)
	}
	if info.Catalog.Animals == 0 || len(info.Catalog.Classes) == 0 {
		t.Errorf("пустая сводка по справочнику: %+v", info.Catalog)
	}
	if info.Catalog.Database == "" {
		t.Error("server_info не назвал файл базы")
	}
	if info.Calls["random_animal"] != 2 {
		t.Errorf("random_animal вызван %d раз, ожидалось 2", info.Calls["random_animal"])
	}
	// server_info считает и сам себя: middleware срабатывает до обработчика.
	if info.TotalCalls != 3 {
		t.Errorf("всего вызовов %d, ожидалось 3", info.TotalCalls)
	}
}

// fakeSources поднимает подставные Википедию и GBIF: тесты онлайн-части
// не должны зависеть ни от сети, ни от чужих серверов.
func fakeSources(t *testing.T) Options {
	t.Helper()

	wiki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")

		if q.Get("list") == "search" {
			w.Write([]byte(`{"query":{"search":[
				{"title":"Обыкновенная рысь","snippet":"<span>Рысь</span> — вид кошачьих"},
				{"title":"Рыси","snippet":"род кошачьих"}]}}`))
			return
		}
		w.Write([]byte(`{"query":{"redirects":[{"from":"Рысь","to":"Обыкновенная рысь"}],
			"pages":[{"title":"Обыкновенная рысь","extract":"Рысь — хищное млекопитающее.\n\n== Питание ==\nОснову рациона составляют зайцы.\n\n== Распространение ==\nТайга."}]}}`))
	}))
	t.Cleanup(wiki.Close)

	gbif := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/species/match"):
			if r.URL.Query().Get("name") == "Lynx lynx" {
				w.Write([]byte(`{"usageKey":2435240,"scientificName":"Lynx lynx (Linnaeus, 1758)",
					"canonicalName":"Lynx lynx","rank":"SPECIES","status":"ACCEPTED",
					"confidence":99,"matchType":"EXACT","kingdom":"Animalia","class":"Mammalia"}`))
				return
			}
			w.Write([]byte(`{"matchType":"NONE","confidence":0}`))
		case strings.HasSuffix(r.URL.Path, "/parents"):
			w.Write([]byte(`[{"key":1,"rank":"KINGDOM","canonicalName":"Animalia"},
				{"key":2,"rank":"GENUS","canonicalName":"Lynx"}]`))
		case strings.HasSuffix(r.URL.Path, "/vernacularNames"):
			w.Write([]byte(`{"results":[{"vernacularName":"Рысь обыкновенная","language":"rus"},
				{"vernacularName":"Eurasian Lynx","language":"eng"},
				{"vernacularName":"рысь обыкновенная","language":"rus"}]}`))
		default:
			w.Write([]byte(`{"key":2435240,"rank":"SPECIES","canonicalName":"Lynx lynx"}`))
		}
	}))
	t.Cleanup(gbif.Close)

	f := sources.NewFetcher()
	return Options{
		Wikipedia: sources.NewWikipedia(wiki.URL, f),
		GBIF:      sources.NewGBIF(gbif.URL, f),
	}
}

func TestWikipediaTools(t *testing.T) {
	session := connect(t, fakeSources(t))

	t.Run("поиск", func(t *testing.T) {
		var out WikiSearchOut
		call(t, session, "search_wikipedia", map[string]any{"query": "рысь"}, &out)
		if out.Count != 2 {
			t.Fatalf("нашлось %d статей", out.Count)
		}
		if strings.Contains(out.Results[0].Snippet, "<span>") {
			t.Errorf("разметка не вычищена: %q", out.Results[0].Snippet)
		}
	})

	t.Run("предел выдачи", func(t *testing.T) {
		var out WikiSearchOut
		call(t, session, "search_wikipedia", map[string]any{"query": "рысь", "limit": 1}, &out)
		if out.Count != 1 {
			t.Fatalf("при limit=1 вернулось %d статей", out.Count)
		}
	})

	t.Run("статья без раздела", func(t *testing.T) {
		var out WikiReadOut
		call(t, session, "read_wikipedia", map[string]any{"title": "Рысь"}, &out)
		if out.Title != "Обыкновенная рысь" || out.RedirectedFrom != "Рысь" {
			t.Errorf("перенаправление не раскрылось: %+v", out)
		}
		if !slices.Equal(out.Sections, []string{"Питание", "Распространение"}) {
			t.Errorf("оглавление %v", out.Sections)
		}
		if out.Text != "" {
			t.Error("без запроса раздела пришёл текст раздела")
		}
	})

	t.Run("раздел", func(t *testing.T) {
		var out WikiReadOut
		call(t, session, "read_wikipedia", map[string]any{"title": "Обыкновенная рысь", "section": "питание"}, &out)
		if out.Found == nil || !*out.Found {
			t.Fatalf("раздел не нашёлся: %+v", out)
		}
		if !strings.Contains(out.Text, "зайцы") {
			t.Errorf("текст раздела: %q", out.Text)
		}
	})

	t.Run("раздела нет", func(t *testing.T) {
		var out WikiReadOut
		call(t, session, "read_wikipedia", map[string]any{"title": "Обыкновенная рысь", "section": "Размножение"}, &out)
		if out.Found == nil || *out.Found {
			t.Fatalf("несуществующий раздел объявлен найденным: %+v", out)
		}
		if out.Hint == "" || len(out.Sections) == 0 {
			t.Error("нет подсказки со списком разделов")
		}
	})
}

func TestGBIFTools(t *testing.T) {
	session := connect(t, fakeSources(t))

	var match sources.Match
	call(t, session, "match_taxon", map[string]any{"scientific_name": "Lynx lynx"}, &match)
	if !match.Found || match.UsageKey != 2435240 {
		t.Fatalf("сверка не прошла: %+v", match)
	}

	var missing sources.Match
	call(t, session, "match_taxon", map[string]any{"scientific_name": "Lynx draconis"}, &missing)
	if missing.Found || missing.Note == "" {
		t.Errorf("выдуманный таксон подтверждён: %+v", missing)
	}

	// Дерево по латинскому названию: ключ сервер находит сам.
	var tree TreeOut
	call(t, session, "taxon_tree", map[string]any{"scientific_name": "Lynx lynx"}, &tree)
	if tree.UsageKey != 2435240 || len(tree.Tree) != 3 {
		t.Fatalf("дерево: %+v", tree)
	}
	if tree.Tree[0].RankRu != "царство" || tree.Tree[len(tree.Tree)-1].Name != "Lynx lynx" {
		t.Errorf("дерево собрано неверно: %+v", tree.Tree)
	}

	res := call(t, session, "taxon_tree", map[string]any{"scientific_name": "Lynx draconis"}, nil)
	if !res.IsError {
		t.Error("дерево построено для неподтверждённого таксона")
	}

	var names VernacularOut
	call(t, session, "vernacular_names", map[string]any{"usage_key": 2435240}, &names)
	if names.Language != "rus" || names.Count != 1 {
		t.Fatalf("народные названия: %+v", names)
	}
}

func TestStructuredContentMatchesText(t *testing.T) {
	session := connect(t, Options{})

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "server_info"})
	if err != nil {
		t.Fatalf("вызов не прошёл: %v", err)
	}
	if res.StructuredContent == nil {
		t.Fatal("сервер не прислал структурный результат")
	}

	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("структурный результат не сериализуется: %v", err)
	}
	var fromText, fromStructured map[string]any
	if err := json.Unmarshal([]byte(text(res)), &fromText); err != nil {
		t.Fatalf("текстовый результат не разобрался: %v", err)
	}
	if err := json.Unmarshal(structured, &fromStructured); err != nil {
		t.Fatalf("структурный результат не разобрался: %v", err)
	}
	if fromText["server"] != fromStructured["server"] {
		t.Errorf("текст и структура разошлись: %v и %v", fromText["server"], fromStructured["server"])
	}
}

// Инструменты записи проверяются через протокол целиком: вызов меняет
// базу, а следующий вызов на чтение это видит. Сервер в тесте держит
// свою временную базу, так что портить нечего.
func TestWriteTools(t *testing.T) {
	session := connect(t, Options{})

	newcomer := map[string]any{
		"id":              "platypus",
		"name":            "Утконос",
		"scientific_name": "Ornithorhynchus anatinus",
		"class":           "млекопитающие",
		"diet":            "хищник",
		"habitats":        []any{"река"},
		"regions":         []any{"Австралия"},
		"food":            []any{"личинки", "черви"},
		"weight_kg":       map[string]any{"min": 0.7, "max": 2.4},
		"lifespan_years":  17,
	}

	t.Run("добавление", func(t *testing.T) {
		var before, after ServerInfo
		call(t, session, "server_info", nil, &before)

		var out WriteOut
		call(t, session, "add_animal", newcomer, &out)
		if out.Done != "added" || out.Animal == nil || out.Animal.ID != "platypus" {
			t.Fatalf("получили %+v", out)
		}
		if len(out.Animal.Food) != 2 || out.Animal.WeightKg.Max != 2.4 {
			t.Errorf("запись сохранилась не полностью: %+v", out.Animal)
		}

		call(t, session, "server_info", nil, &after)
		if after.Catalog.Animals != before.Catalog.Animals+1 {
			t.Errorf("записей стало %d, было %d", after.Catalog.Animals, before.Catalog.Animals)
		}

		// Новая запись должна находиться обычным чтением.
		var got GetOut
		call(t, session, "get_animal", map[string]any{"animal": "Утконос"}, &got)
		if !got.Found {
			t.Error("добавленная запись не читается через get_animal")
		}
	})

	t.Run("повторное добавление отклоняется", func(t *testing.T) {
		res := call(t, session, "add_animal", newcomer, nil)
		if !res.IsError {
			t.Fatal("сервер переписал существующую запись")
		}
		if !strings.Contains(text(res), "update_animal") {
			t.Errorf("в отказе нет подсказки: %s", text(res))
		}
	})

	t.Run("изменение", func(t *testing.T) {
		var out WriteOut
		call(t, session, "update_animal", map[string]any{
			"animal":              "platypus",
			"conservation_status": "VU",
			"food":                []any{"личинки"},
		}, &out)

		if out.Done != "updated" || out.Animal.Conservation != "VU" {
			t.Fatalf("получили %+v", out)
		}
		if len(out.Animal.Food) != 1 {
			t.Errorf("список заменился не целиком: %v", out.Animal.Food)
		}
		// Непереданные поля остаются прежними.
		if out.Animal.Name != "Утконос" || len(out.Animal.Habitats) != 1 {
			t.Errorf("изменение задело чужие поля: %+v", out.Animal)
		}
	})

	t.Run("изменение без полей", func(t *testing.T) {
		res := call(t, session, "update_animal", map[string]any{"animal": "platypus"}, nil)
		if !res.IsError {
			t.Fatal("пустое изменение прошло")
		}
	})

	t.Run("удаление", func(t *testing.T) {
		var out WriteOut
		call(t, session, "delete_animal", map[string]any{"animal": "Утконос"}, &out)
		if out.Done != "deleted" || out.Animal == nil || out.Animal.ID != "platypus" {
			t.Fatalf("получили %+v", out)
		}

		var got GetOut
		call(t, session, "get_animal", map[string]any{"animal": "platypus"}, &got)
		if got.Found {
			t.Error("удалённая запись всё ещё читается")
		}

		res := call(t, session, "delete_animal", map[string]any{"animal": "platypus"}, nil)
		if !res.IsError {
			t.Fatal("повторное удаление прошло без ошибки")
		}
	})
}

// Аннотации инструментов — то, по чему клиент решает, спрашивать ли
// разрешения у человека. Ошибка здесь тише всего и опаснее всего.
func TestWriteToolsAreAnnotated(t *testing.T) {
	session := connect(t, Options{})

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("список инструментов не получен: %v", err)
	}

	writers := map[string]bool{"add_animal": false, "update_animal": false, "delete_animal": true}
	for _, tool := range res.Tools {
		destructive, isWriter := writers[tool.Name]
		if tool.Annotations == nil {
			t.Errorf("%s: нет аннотаций", tool.Name)
			continue
		}
		if isWriter {
			if tool.Annotations.ReadOnlyHint {
				t.Errorf("%s: помечен как readOnly, хотя меняет базу", tool.Name)
			}
			got := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
			if got != destructive {
				t.Errorf("%s: destructiveHint=%v, ожидалось %v", tool.Name, got, destructive)
			}
			continue
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: читающий инструмент не помечен readOnly", tool.Name)
		}
	}
}
