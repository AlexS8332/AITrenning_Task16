// Команда animals-mcp — MCP-сервер справочника по животным.
//
// Сервер говорит по протоколу MCP через стандартный ввод-вывод: клиент
// запускает его как дочерний процесс, пишет запросы в stdin и читает
// ответы из stdout. Запускать вручную смысла нет — он будет молча ждать
// JSON-RPC на входе; чтобы посмотреть, что он умеет, запустите клиент
// mcp-list.
//
// Справочник хранится в базе SQLite. Файл создаётся при первом запуске
// и наполняется начальными данными; дальше он живёт своей жизнью, и
// добавленные через инструменты записи переживают перезапуск.
//
// Поскольку stdout занят протоколом, всё человекочитаемое уходит только
// в stderr, и только с флагом -v.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/AlexS8332/AITrenning_Task16/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version сообщается клиенту в ответе initialize.
const version = "1.1.0"

func main() {
	verbose := flag.Bool("v", false, "писать журнал вызовов инструментов в stderr")
	dbPath := flag.String("db", "", "файл базы данных; по умолчанию animals.db рядом с исполняемым файлом, :memory: — база в памяти")
	flag.Parse()

	logger := slog.New(slog.DiscardHandler)
	if *verbose {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	path, err := databasePath(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "не удалось выбрать файл базы:", err)
		os.Exit(1)
	}

	// Ctrl+C закрывает соединение так же, как закрытый клиентом stdin.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	srv, err := mcpserver.New(ctx, mcpserver.Options{
		Version: version,
		DBPath:  path,
		Logger:  logger,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "сервер не собрался:", err)
		os.Exit(1)
	}
	defer srv.Close()

	logger.Info("сервер запущен", "transport", "stdio", "version", version, "db", path)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "сервер остановлен с ошибкой:", err)
		os.Exit(1)
	}
}

// databasePath выбирает файл базы. По умолчанию — рядом с исполняемым
// файлом, а не в текущем каталоге: MCP-клиенты запускают сервер с
// произвольным рабочим каталогом, и база расползлась бы по диску.
func databasePath(flagValue string) (string, error) {
	switch flagValue {
	case "":
	case ":memory:":
		return mcpserver.MemoryDB, nil
	default:
		return flagValue, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)

	// `go run` собирает программу во временный каталог и оттуда же
	// запускает. База, положенная рядом с таким бинарником, исчезла бы
	// вместе с ним, поэтому при запуске из кэша сборки кладём её в
	// рабочий каталог — то есть в корень репозитория.
	if tmp := os.TempDir(); strings.HasPrefix(dir, tmp) || strings.Contains(dir, "go-build") {
		return "animals.db", nil
	}
	return filepath.Join(dir, "animals.db"), nil
}
