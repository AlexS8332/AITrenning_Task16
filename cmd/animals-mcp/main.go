// Команда animals-mcp — MCP-сервер справочника по животным.
//
// Сервер говорит по протоколу MCP через стандартный ввод-вывод: клиент
// запускает его как дочерний процесс, пишет запросы в stdin и читает
// ответы из stdout. Запускать вручную смысла нет — он будет молча ждать
// JSON-RPC на входе; чтобы посмотреть, что он умеет, запустите клиент
// mcp-list.
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

	"github.com/AlexS8332/AITrenning_Task16/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version сообщается клиенту в ответе initialize.
const version = "1.0.0"

func main() {
	verbose := flag.Bool("v", false, "писать журнал вызовов инструментов в stderr")
	flag.Parse()

	logger := slog.New(slog.DiscardHandler)
	if *verbose {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	srv, err := mcpserver.New(mcpserver.Options{Version: version, Logger: logger})
	if err != nil {
		fmt.Fprintln(os.Stderr, "сервер не собрался:", err)
		os.Exit(1)
	}

	// Ctrl+C закрывает соединение так же, как закрытый клиентом stdin.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger.Info("сервер запущен", "transport", "stdio", "version", version)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "сервер остановлен с ошибкой:", err)
		os.Exit(1)
	}
}
