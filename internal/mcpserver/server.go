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

// MemoryDB — путь для базы, живущей только в памяти процесса.
const MemoryDB = catalog.MemoryPath

// Options — из чего собрать сервер. Все поля необязательны: база
// открывается по пути DBPath (по умолчанию — в памяти), источники
// создаются сами, логгер молчит.
type Options struct {
	Version string
	// Store — уже открытая база. Если не передана, сервер откроет её
	// сам по DBPath и закроет вместе с собой.
	Store     *catalog.Store
	DBPath    string
	Wikipedia *sources.Wikipedia
	GBIF      *sources.GBIF
	Logger    *slog.Logger
}

// Server — MCP-сервер со своим состоянием. Состояние здесь одно:
// счётчик вызовов, который отдаёт инструмент server_info. По нему
// видно, что соединение не просто установлено, а работает.
type Server struct {
	mcp     *mcp.Server
	cat     *catalog.Store
	ownsDB  bool
	wiki    *sources.Wikipedia
	gbif    *sources.GBIF
	log     *slog.Logger
	version string
	started time.Time
	names   []string

	mu    sync.Mutex
	calls map[string]int
}

// New собирает сервер со всеми инструментами и открывает базу.
func New(ctx context.Context, o Options) (*Server, error) {
	cat, ownsDB := o.Store, false
	if cat == nil {
		path := o.DBPath
		if path == "" {
			path = catalog.MemoryPath
		}
		var err error
		if cat, err = catalog.Open(ctx, path); err != nil {
			return nil, err
		}
		ownsDB = true
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
		ownsDB:  ownsDB,
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
		Instructions: "Сервер отвечает на вопросы о животных. Справочник хранится в базе " +
			"SQLite: инструменты list_/get_/search_/compare_/random_ читают её и работают " +
			"всегда, add_/update_/delete_animal её меняют. Инструменты wikipedia- и taxon- " +
			"ходят в русскую Википедию и в таксономическую базу GBIF и требуют сети.",
	})

	// Счётчик вызовов и журнал — одним middleware: сервер общается по
	// stdin/stdout, поэтому писать в stdout нельзя, только в логгер.
	s.mcp.AddReceivingMiddleware(s.countCalls)

	s.addCatalogTools()
	s.addWriteTools()
	s.addSourceTools()
	s.addInfoTool()
	return s, nil
}

// Close закрывает базу, если сервер открывал её сам. Переданную извне
// базу закрывает тот, кто её открыл.
func (s *Server) Close() error {
	if s.ownsDB {
		return s.cat.Close()
	}
	return nil
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
	Database string   `json:"database" jsonschema:"путь к файлу базы данных"`
	Animals  int      `json:"animals" jsonschema:"число записей в справочнике"`
	Classes  []string `json:"classes" jsonschema:"классы животных"`
	Diets    []string `json:"diets" jsonschema:"типы питания"`
	Habitats []string `json:"habitats" jsonschema:"среды обитания"`
}

// catalogInfo собирает сводку по базе. Четыре запроса вместо одного:
// сводка нужна редко, а читаемость важнее.
func (s *Server) catalogInfo(ctx context.Context) (CatalogInfo, error) {
	info := CatalogInfo{Database: s.cat.Path()}

	var err error
	if info.Animals, err = s.cat.Count(ctx); err != nil {
		return CatalogInfo{}, err
	}
	if info.Classes, err = s.cat.Classes(ctx); err != nil {
		return CatalogInfo{}, err
	}
	if info.Diets, err = s.cat.Diets(ctx); err != nil {
		return CatalogInfo{}, err
	}
	if info.Habitats, err = s.cat.Habitats(ctx); err != nil {
		return CatalogInfo{}, err
	}
	return info, nil
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
		info, err := s.catalogInfo(ctx)
		if err != nil {
			return nil, ServerInfo{}, err
		}
		calls, total := s.callStats()
		return nil, ServerInfo{
			Server:  Name,
			Version: s.version,
			Tools:   s.toolNames(),
			Catalog: info,
			Sources: []SourceInfo{
				{Name: "справочник в SQLite", BaseURL: s.cat.Path(), NeedsNet: false},
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
