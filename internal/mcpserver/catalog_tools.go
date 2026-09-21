package mcpserver

import (
	"context"
	"strings"

	"github.com/AlexS8332/AITrenning_Task16/internal/catalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Инструменты локального справочника. Они не ходят в сеть, поэтому
// сервер остаётся полезным и проверяемым на машине без интернета.

const defaultListLimit = 20

// ListIn — аргументы list_animals.
type ListIn struct {
	Class   string `json:"class,omitempty" jsonschema:"класс животного; допустимые значения возвращает server_info"`
	Habitat string `json:"habitat,omitempty" jsonschema:"среда обитания или регион, поиск по вхождению: «лес» найдёт «смешанный лес»"`
	Limit   int    `json:"limit,omitempty" jsonschema:"сколько записей вернуть, 1..100; по умолчанию 20"`
	Offset  int    `json:"offset,omitempty" jsonschema:"сколько записей пропустить; по умолчанию 0"`
}

// ListOut — результат list_animals.
type ListOut struct {
	Total   int             `json:"total" jsonschema:"сколько записей подошло под условия"`
	Offset  int             `json:"offset" jsonschema:"сколько записей пропущено"`
	Count   int             `json:"count" jsonschema:"сколько записей в этом ответе"`
	Animals []catalog.Brief `json:"animals" jsonschema:"краткие записи; подробности даёт get_animal"`
}

// GetIn — аргументы get_animal.
type GetIn struct {
	Animal string `json:"animal" jsonschema:"идентификатор, русское или латинское название животного"`
}

// GetOut — результат get_animal. Отсутствие записи — обычный ответ, а не
// ошибка: клиенту нужен список того, что в справочнике есть.
type GetOut struct {
	Found   bool            `json:"found" jsonschema:"нашлась ли запись"`
	Animal  *catalog.Animal `json:"animal,omitempty" jsonschema:"полная карточка животного"`
	Hint    string          `json:"hint,omitempty" jsonschema:"что делать, если запись не нашлась"`
	Similar []catalog.Brief `json:"similar,omitempty" jsonschema:"похожие записи справочника"`
}

// SearchIn — аргументы search_animals.
type SearchIn struct {
	Query       string   `json:"query,omitempty" jsonschema:"свободный запрос: слово ищется в названиях, описании, пище и регионах"`
	Diet        string   `json:"diet,omitempty" jsonschema:"тип питания, например «хищник» или «травоядное»"`
	Habitat     string   `json:"habitat,omitempty" jsonschema:"среда обитания или регион"`
	MinWeightKg *float64 `json:"min_weight_kg,omitempty" jsonschema:"нижняя граница веса в килограммах"`
	MaxWeightKg *float64 `json:"max_weight_kg,omitempty" jsonschema:"верхняя граница веса в килограммах"`
}

// SearchOut — результат search_animals.
type SearchOut struct {
	Count   int             `json:"count" jsonschema:"сколько записей нашлось"`
	Animals []catalog.Brief `json:"animals" jsonschema:"найденные записи"`
}

// CompareIn — аргументы compare_animals.
type CompareIn struct {
	Animals []string `json:"animals" jsonschema:"от двух до четырёх животных: идентификаторы или названия"`
	Fields  []string `json:"fields,omitempty" jsonschema:"какие поля сравнивать; по умолчанию все"`
}

// CompareRow — одна строка таблицы сравнения.
type CompareRow struct {
	Field  string   `json:"field" jsonschema:"название поля"`
	Values []string `json:"values" jsonschema:"значения поля в порядке колонок"`
	Same   bool     `json:"same" jsonschema:"совпадает ли значение у всех животных"`
}

// CompareOut — результат compare_animals.
type CompareOut struct {
	Columns []string     `json:"columns" jsonschema:"названия животных в порядке колонок"`
	Rows    []CompareRow `json:"rows" jsonschema:"строки таблицы сравнения"`
}

// RandomOut — результат random_animal.
type RandomOut struct {
	Animal catalog.Animal `json:"animal" jsonschema:"случайная карточка справочника"`
}

func (s *Server) addCatalogTools() {
	addTool(s, &mcp.Tool{
		Name:  "list_animals",
		Title: "Список животных",
		Description: "Список животных локального справочника с необязательными фильтрами по классу " +
			"и среде обитания. Возвращает краткие записи; подробности даёт get_animal.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listAnimals)

	addTool(s, &mcp.Tool{
		Name:  "get_animal",
		Title: "Карточка животного",
		Description: "Полная карточка животного из локального справочника по идентификатору, " +
			"русскому или латинскому названию. Если записи нет, возвращает found=false " +
			"и похожие записи, а не ошибку.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getAnimal)

	addTool(s, &mcp.Tool{
		Name:  "search_animals",
		Title: "Поиск по справочнику",
		Description: "Поиск по локальному справочнику: свободный запрос, тип питания, среда обитания " +
			"и границы веса. Условия складываются. Вес сравнивается по пересечению диапазонов: " +
			"животное весом 18–30 кг подходит и под min_weight_kg=25, и под max_weight_kg=20.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.searchAnimals)

	addTool(s, &mcp.Tool{
		Name:  "compare_animals",
		Title: "Сравнение животных",
		Description: "Сравнение двух-четырёх животных справочника по выбранным полям. " +
			"Возвращает таблицу: строка на поле, колонка на животное, с пометкой совпадающих значений.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.compareAnimals)

	addTool(s, &mcp.Tool{
		Name:        "random_animal",
		Title:       "Случайное животное",
		Description: "Случайная карточка из локального справочника. Аргументов нет.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.randomAnimal)
}

func (s *Server) listAnimals(ctx context.Context, req *mcp.CallToolRequest, in ListIn) (*mcp.CallToolResult, ListOut, error) {
	found := s.cat.Find(catalog.Filter{Class: in.Class, Habitat: in.Habitat})

	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > 100 {
		limit = 100
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(found) {
		offset = len(found)
	}
	end := min(offset+limit, len(found))

	page := make([]catalog.Brief, 0, end-offset)
	for _, a := range found[offset:end] {
		page = append(page, a.Brief())
	}
	return nil, ListOut{Total: len(found), Offset: offset, Count: len(page), Animals: page}, nil
}

func (s *Server) getAnimal(ctx context.Context, req *mcp.CallToolRequest, in GetIn) (*mcp.CallToolResult, GetOut, error) {
	name := strings.TrimSpace(in.Animal)
	if name == "" {
		return nil, GetOut{}, errf("animal пуст: укажите идентификатор или название животного")
	}
	if a, ok := s.cat.Get(name); ok {
		return nil, GetOut{Found: true, Animal: &a}, nil
	}

	// Не нашли по точному совпадению — отдаём то, что похоже. Так клиент
	// видит границы справочника и не выдумывает ответ сам.
	similar := briefs(s.cat.Find(catalog.Filter{Query: name}))
	out := GetOut{
		Found:   false,
		Similar: similar,
		Hint:    "в локальном справочнике такой записи нет; попробуйте list_animals или поиск во внешних источниках через search_wikipedia",
	}
	if len(similar) > 0 {
		out.Hint = "точного совпадения нет; возможно, вы имели в виду одну из записей в поле similar"
	}
	return nil, out, nil
}

func (s *Server) searchAnimals(ctx context.Context, req *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, SearchOut, error) {
	found := s.cat.Find(catalog.Filter{
		Query:       in.Query,
		Diet:        in.Diet,
		Habitat:     in.Habitat,
		MinWeightKg: in.MinWeightKg,
		MaxWeightKg: in.MaxWeightKg,
	})
	return nil, SearchOut{Count: len(found), Animals: briefs(found)}, nil
}

func (s *Server) compareAnimals(ctx context.Context, req *mcp.CallToolRequest, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	if len(in.Animals) < 2 || len(in.Animals) > 4 {
		return nil, CompareOut{}, errf("сравнивать можно от двух до четырёх животных, передано %d", len(in.Animals))
	}

	animals := make([]catalog.Animal, 0, len(in.Animals))
	columns := make([]string, 0, len(in.Animals))
	for _, name := range in.Animals {
		a, ok := s.cat.Get(name)
		if !ok {
			return nil, CompareOut{}, errf("животного «%s» нет в справочнике; список даёт list_animals", name)
		}
		animals = append(animals, a)
		columns = append(columns, a.Name)
	}

	fields := in.Fields
	if len(fields) == 0 {
		fields = catalog.FieldNames
	}

	rows := make([]CompareRow, 0, len(fields))
	for _, field := range fields {
		values := make([]string, 0, len(animals))
		same := true
		for _, a := range animals {
			v, ok := a.Field(field)
			if !ok {
				return nil, CompareOut{}, errf("поля «%s» нет; доступны: %s", field, strings.Join(catalog.FieldNames, ", "))
			}
			if len(values) > 0 && v != values[0] {
				same = false
			}
			values = append(values, v)
		}
		rows = append(rows, CompareRow{Field: field, Values: values, Same: same})
	}
	return nil, CompareOut{Columns: columns, Rows: rows}, nil
}

func (s *Server) randomAnimal(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, RandomOut, error) {
	return nil, RandomOut{Animal: s.cat.Random()}, nil
}

func briefs(animals []catalog.Animal) []catalog.Brief {
	out := make([]catalog.Brief, 0, len(animals))
	for _, a := range animals {
		out = append(out, a.Brief())
	}
	return out
}
