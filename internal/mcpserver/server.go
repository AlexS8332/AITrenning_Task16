// Package mcpserver собирает MCP-сервер справочника по животным:
// объявляет инструменты, разбирает их аргументы и отдаёт результаты.
//
// Сервер ничего не знает о том, кто к нему подключился — модель, клиент
// из cmd/mcp-list или Claude Desktop. Его задача одна: честно описать
// свои инструменты в ответе tools/list и исполнить tools/call.
package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/AlexS8332/AITrenning_Task16/internal/catalog"
	"github.com/AlexS8332/AITrenning_Task16/internal/sources"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Name — имя сервера, которое клиент видит в ответе initialize.
const Name = "animals-reference"

// Options — из чего собрать сервер. Все поля необязательны: справочник и
// источники создаются по умолчанию, логгер молчит.
type Options struct {
	Version   string
	Catalog   *catalog.Catalog
	Wikipedia *sources.Wikipedia
	GBIF      *sources.GBIF
	Logger    *slog.Logger
}

// Server — MCP-сервер со своим состоянием. Состояние здесь одно:
// счётчик вызовов, который отдаёт инструмент server_info. По нему
// видно, что соединение не просто установлено, а работает.
type Server struct {
	mcp     *mcp.Server
	cat     *catalog.Catalog
	wiki    *sources.Wikipedia
	gbif    *sources.GBIF
	log     *slog.Logger
	version string
	started time.Time
	names   []string

	mu    sync.Mutex
	calls map[string]int
}

// New собирает сервер со всеми инструментами.
func New(o Options) (*Server, error) {
	cat := o.Catalog
	if cat == nil {
		var err error
		if cat, err = catalog.Load(); err != nil {
			return nil, err
		}
	}
	wiki, gbif := o.Wikipedia, o.GBIF
	if wiki == nil || gbif == nil {
		f := sources.NewFetcher()
		if wiki == nil {
			wiki = sources.NewWikipedia("", f)
		}
		if gbif == nil {
			gbif = sources.NewGBIF("", f)
		}
	}
	version := o.Version
	if version == "" {
		version = "dev"
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	s := &Server{
		cat:     cat,
		wiki:    wiki,
		gbif:    gbif,
		log:     log,
		version: version,
		started: time.Now(),
		calls:   make(map[string]int),
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{
		Name:    Name,
		Version: version,
		Title:   "Справочник по животным",
	}, &mcp.ServerOptions{
		Instructions: "Сервер отвечает на вопросы о животных. Инструменты с префиксом " +
			"list_/get_/search_/compare_/random_ работают по локальному справочнику и " +
			"доступны всегда; wikipedia- и taxon-инструменты ходят в русскую Википедию " +
			"и в таксономическую базу GBIF и требуют сети.",
	})

	// Счётчик вызовов и журнал — одним middleware: сервер общается по
	// stdin/stdout, поэтому писать в stdout нельзя, только в логгер.
	s.mcp.AddReceivingMiddleware(s.countCalls)

	s.addCatalogTools()
	s.addSourceTools()
	s.addInfoTool()
	return s, nil
}

// MCP возвращает сервер SDK: нужен тестам и транспортам.
func (s *Server) MCP() *mcp.Server { return s.mcp }

// Run обслуживает одно подключение до его закрытия.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	return s.mcp.Run(ctx, t)
}

func (s *Server) countCalls(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if !ok {
			return next(ctx, method, req)
		}

		s.mu.Lock()
		s.calls[call.Params.Name]++
		s.mu.Unlock()

		start := time.Now()
		res, err := next(ctx, method, req)
		s.log.Info("вызов инструмента",
			"tool", call.Params.Name,
			"args", string(call.Params.Arguments),
			"ms", time.Since(start).Milliseconds(),
			"err", err)
		return res, err
	}
}

// callStats — копия счётчика вызовов.
func (s *Server) callStats() (map[string]int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]int, len(s.calls))
	total := 0
	for name, n := range s.calls {
		out[name] = n
		total += n
	}
	return out, total
}

// SourceInfo — описание внешнего источника для server_info.
type SourceInfo struct {
	Name     string `json:"name" jsonschema:"название источника"`
	BaseURL  string `json:"base_url" jsonschema:"адрес API источника"`
	NeedsNet bool   `json:"needs_network" jsonschema:"нужен ли сетевой доступ"`
}

// CatalogInfo — сводка по локальному справочнику.
type CatalogInfo struct {
	Animals  int      `json:"animals" jsonschema:"число записей в справочнике"`
	Classes  []string `json:"classes" jsonschema:"классы животных"`
	Diets    []string `json:"diets" jsonschema:"типы питания"`
	Habitats []string `json:"habitats" jsonschema:"среды обитания"`
}

// ServerInfo — результат инструмента server_info.
type ServerInfo struct {
	Server        string         `json:"server" jsonschema:"имя сервера"`
	Version       string         `json:"version" jsonschema:"версия сервера"`
	Tools         []string       `json:"tools" jsonschema:"имена всех инструментов"`
	Catalog       CatalogInfo    `json:"catalog" jsonschema:"сводка по локальному справочнику"`
	Sources       []SourceInfo   `json:"sources" jsonschema:"внешние источники"`
	Calls         map[string]int `json:"calls" jsonschema:"сколько раз вызывали каждый инструмент за сессию"`
	TotalCalls    int            `json:"total_calls" jsonschema:"всего вызовов за сессию"`
	UptimeSeconds int            `json:"uptime_seconds" jsonschema:"сколько секунд работает сервер"`
}

func (s *Server) addInfoTool() {
	addTool(s, &mcp.Tool{
		Name:  "server_info",
		Title: "Сведения о сервере",
		Description: "Версия сервера, список его инструментов, сводка по локальному справочнику, " +
			"подключённые источники и счётчик вызовов за текущую сессию. " +
			"Аргументов нет.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ServerInfo, error) {
		calls, total := s.callStats()
		return nil, ServerInfo{
			Server:  Name,
			Version: s.version,
			Tools:   s.toolNames(),
			Catalog: CatalogInfo{
				Animals:  s.cat.Len(),
				Classes:  s.cat.Classes(),
				Diets:    s.cat.Diets(),
				Habitats: s.cat.Habitats(),
			},
			Sources: []SourceInfo{
				{Name: "локальный справочник", BaseURL: "", NeedsNet: false},
				{Name: "Википедия (русская)", BaseURL: s.wiki.Base, NeedsNet: true},
				{Name: "GBIF", BaseURL: s.gbif.Base, NeedsNet: true},
			},
			Calls:         calls,
			TotalCalls:    total,
			UptimeSeconds: int(time.Since(s.started).Seconds()),
		}, nil
	})
}

// addTool регистрирует инструмент и запоминает его имя. Сервер SDK не
// отдаёт свои инструменты списком, а server_info должен их назвать —
// проще вести список при регистрации, чем спрашивать сервер о себе.
//
// Это свободная функция, а не метод: методы в Go не бывают обобщёнными,
// а типы аргументов и результата у каждого инструмента свои.
func addTool[In, Out any](s *Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	s.names = append(s.names, t.Name)
	mcp.AddTool(s.mcp, t, h)
}

// toolNames — имена инструментов по алфавиту.
func (s *Server) toolNames() []string {
	names := make([]string, len(s.names))
	copy(names, s.names)
	sort.Strings(names)
	return names
}

// errf — ошибка инструмента с понятным текстом. Такая ошибка приезжает
// клиенту как результат вызова с IsError, а не как сбой протокола:
// модель должна суметь прочитать её и исправиться.
func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
