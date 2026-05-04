// Auth Service 独立二进制；仅装载 Auth；wiring 在 internal/cmd/auth.Run。
//
// 推荐入口：./qqmusic-gateway （一键全栈）。本二进制仅供单服务部署使用。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	authcmd "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/auth"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := authcmd.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
