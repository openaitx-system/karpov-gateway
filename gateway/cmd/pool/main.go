// Pool Service 独立二进制；推荐入口 ./qqmusic-gateway （一键全栈）。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	poolcmd "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/pool"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := poolcmd.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
